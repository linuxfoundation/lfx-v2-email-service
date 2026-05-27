// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	smtpinfra "github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/smtp"
	sqsinfra "github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/sqs"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

const gracefulShutdownSeconds = 25

func main() {
	logging.InitStructuredLogConfig()
	env := parseEnv()

	ctx, cancel := context.WithCancel(context.Background())

	var sender domain.Sender
	if env.EmailEnabled {
		sender = smtpinfra.NewSMTPSender(smtpinfra.Config{
			Host:             env.SMTP.Host,
			Port:             env.SMTP.Port,
			From:             env.SMTP.From,
			Username:         env.SMTP.Username,
			Password:         env.SMTP.Password,
			ConfigurationSet: env.SESConfigurationSet,
		})
		slog.Info("email sender ready", "smtp_host", env.SMTP.Host, "smtp_port", env.SMTP.Port)
		if env.SESConfigurationSet != "" {
			slog.Info("SES configuration set enabled", "configuration_set", env.SESConfigurationSet)
		}
	} else {
		sender = smtpinfra.NewNoOpSender()
		slog.Info("email sending disabled (EMAIL_ENABLED=false)")
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup

	nc, recipientsKV, groupIndexKV, err := setupNATSAndKV(ctx, env.NatsURL)
	if err != nil {
		slog.Error("failed to connect to NATS", logging.ErrKey, err)
		cancel()
		os.Exit(1)
	}

	wg.Add(2) // HTTP server + NATS drain
	if err := subscribeHandlers(ctx, nc, sender, recipientsKV, groupIndexKV, &wg, done); err != nil {
		slog.Error("failed to subscribe NATS handlers", logging.ErrKey, err)
		cancel()
		os.Exit(1)
	}

	var pollerAborted atomic.Bool

	if env.SESEventingEnabled {
		if env.SESEngagementSQSURL == "" {
			slog.Error("SES_EVENTING_ENABLED is true but SES_ENGAGEMENT_SQS_QUEUE_URL is not set")
			cancel()
			os.Exit(1)
		}
		if recipientsKV == nil {
			slog.Error("SES_EVENTING_ENABLED is true but NATS KV (email-recipients bucket) is unavailable")
			cancel()
			os.Exit(1)
		}
		awsCfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			slog.Error("failed to load AWS config for SQS poller", logging.ErrKey, err)
			cancel()
			os.Exit(1)
		}
		sqsClient := awssqs.NewFromConfig(awsCfg)
		engagementHandler := service.NewEngagementEventHandler(recipientsKV)
		poller := sqsinfra.NewPoller(sqsClient, env.SESEngagementSQSURL, 3, engagementHandler.Handle)
		wg.Add(1)
		go func() {
			defer wg.Done()
			slog.Info("SQS engagement poller started", "queue_url", env.SESEngagementSQSURL)
			if err := poller.Run(ctx); err != nil {
				slog.Error("SQS engagement poller aborted, requesting shutdown", logging.ErrKey, err)
				pollerAborted.Store(true)
				cancel()
				select {
				case done <- syscall.SIGTERM:
				default:
				}
			}
			slog.Info("SQS engagement poller stopped")
		}()
	}

	httpServer := setupHTTPServer(env.Port, nc)
	go func() {
		slog.Info("HTTP health server listening", "port", env.Port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", logging.ErrKey, err)
		}
	}()

	<-done
	slog.Info("shutdown signal received")

	cancel()

	go func() {
		defer wg.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), gracefulShutdownSeconds*time.Second)
		defer shutCancel()
		_ = httpServer.Shutdown(shutCtx)
		slog.Info("HTTP server stopped")
	}()

	if !nc.IsClosed() && !nc.IsDraining() {
		slog.Info("draining NATS connection")
		_ = nc.Drain()
	}

	wg.Wait()
	slog.Info("email service stopped")
	if pollerAborted.Load() {
		os.Exit(1)
	}
}

func setupNATSAndKV(ctx context.Context, natsURL string) (*natsgo.Conn, natsgo.KeyValue, natsgo.KeyValue, error) {
	nc, err := natsgo.Connect(
		natsURL,
		natsgo.DrainTimeout(gracefulShutdownSeconds*time.Second),
		natsgo.ConnectHandler(func(_ *natsgo.Conn) {
			slog.Info("NATS connection established", "url", natsURL)
		}),
		natsgo.ErrorHandler(func(_ *natsgo.Conn, s *natsgo.Subscription, err error) {
			if s != nil {
				slog.Error("async NATS error", logging.ErrKey, err, "subject", s.Subject)
			} else {
				slog.Error("async NATS error", logging.ErrKey, err)
			}
		}),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		slog.Warn("JetStream not available, KV tracking disabled", logging.ErrKey, err)
		return nc, nil, nil, nil
	}

	recipientsKV, err := js.KeyValue(api.EmailRecipientsKVBucket)
	if err != nil {
		slog.Warn("KV bucket not found, tracking disabled", "bucket", api.EmailRecipientsKVBucket, logging.ErrKey, err)
		return nc, nil, nil, nil
	}

	groupIndexKV, err := js.KeyValue(api.EmailGroupIndexKVBucket)
	if err != nil {
		slog.Warn("KV bucket not found, tracking disabled", "bucket", api.EmailGroupIndexKVBucket, logging.ErrKey, err)
		return nc, nil, nil, nil
	}

	return nc, recipientsKV, groupIndexKV, nil
}

func subscribeHandlers(
	ctx context.Context,
	nc *natsgo.Conn,
	sender domain.Sender,
	recipientsKV, groupIndexKV natsgo.KeyValue,
	wg *sync.WaitGroup,
	done chan os.Signal,
) error {
	msgCtx, msgCancel := context.WithCancel(context.Background())

	nc.SetClosedHandler(func(_ *natsgo.Conn) {
		msgCancel()
		if ctx.Err() == nil {
			slog.Error("NATS connection closed unexpectedly")
			select {
			case done <- syscall.SIGTERM:
			default:
			}
		}
		wg.Done()
	})

	sendHandler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV)
	if _, err := nc.QueueSubscribe(api.SendEmailSubject, api.QueueGroup, func(msg *natsgo.Msg) {
		sendHandler.Handle(msgCtx, msg)
	}); err != nil {
		msgCancel()
		return fmt.Errorf("nats subscribe %s: %w", api.SendEmailSubject, err)
	}
	slog.Info("subscribed to NATS subject", "subject", api.SendEmailSubject, "queue", api.QueueGroup)

	if recipientsKV != nil && groupIndexKV != nil {
		statusHandler := service.NewGetEmailStatusHandler(recipientsKV)
		if _, err := nc.QueueSubscribe(api.GetEmailStatusSubject, api.QueueGroup, func(msg *natsgo.Msg) {
			statusHandler.Handle(msgCtx, msg)
		}); err != nil {
			msgCancel()
			return fmt.Errorf("nats subscribe %s: %w", api.GetEmailStatusSubject, err)
		}
		slog.Info("subscribed to NATS subject", "subject", api.GetEmailStatusSubject)

		analyticsHandler := service.NewGetEmailEngagementAnalyticsHandler(recipientsKV, groupIndexKV)
		if _, err := nc.QueueSubscribe(api.GetEmailEngagementAnalyticsSubject, api.QueueGroup, func(msg *natsgo.Msg) {
			analyticsHandler.Handle(msgCtx, msg)
		}); err != nil {
			msgCancel()
			return fmt.Errorf("nats subscribe %s: %w", api.GetEmailEngagementAnalyticsSubject, err)
		}
		slog.Info("subscribed to NATS subject", "subject", api.GetEmailEngagementAnalyticsSubject)
	}

	return nil
}

func setupHTTPServer(port string, nc *natsgo.Conn) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !nc.IsConnected() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
