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
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	sqsinfra "github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/sqs"
	smtpinfra "github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/smtp"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

const gracefulShutdownSeconds = 25

func main() {
	logging.InitStructuredLogConfig()
	env := parseEnv()

	ctx, cancel := context.WithCancel(context.Background())

	// Build the email sender.
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

	// Connect to NATS.
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup

	nc, js, kvStore, err := setupNATSAndKV(ctx, env.NatsURL)
	if err != nil {
		slog.Error("failed to connect to NATS", logging.ErrKey, err)
		cancel()
		os.Exit(1)
	}
	_ = js

	sendEmailHandler := service.NewSendEmailHandler(sender, kvStore)

	wg.Add(2) // HTTP server + NATS drain
	if err := subscribeHandlers(ctx, nc, sendEmailHandler, kvStore, &wg, done); err != nil {
		slog.Error("failed to subscribe NATS handlers", logging.ErrKey, err)
		cancel()
		os.Exit(1)
	}

	// Start SQS poller if configured.
	if env.SESEngagementSQSURL != "" && kvStore != nil {
		awsCfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			slog.Warn("failed to load AWS config, SQS poller disabled", logging.ErrKey, err)
		} else {
			sqsClient := awssqs.NewFromConfig(awsCfg)
			engagementHandler := service.NewEngagementEventHandler(kvStore, nc)
			poller := sqsinfra.NewPoller(sqsClient, env.SESEngagementSQSURL, engagementHandler.Handle)
			wg.Add(1)
			go func() {
				defer wg.Done()
				slog.Info("SQS engagement poller started", "queue_url", env.SESEngagementSQSURL)
				poller.Run(ctx)
				slog.Info("SQS engagement poller stopped")
			}()
		}
	}

	// Start health HTTP server.
	httpServer := setupHTTPServer(env.Port, nc)
	go func() {
		slog.Info("HTTP health server listening", "port", env.Port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", logging.ErrKey, err)
		}
	}()

	// Wait for shutdown signal.
	<-done
	slog.Info("shutdown signal received")

	cancel()

	// Shutdown HTTP server.
	go func() {
		defer wg.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), gracefulShutdownSeconds*time.Second)
		defer shutCancel()
		_ = httpServer.Shutdown(shutCtx)
		slog.Info("HTTP server stopped")
	}()

	// Drain NATS.
	if !nc.IsClosed() && !nc.IsDraining() {
		slog.Info("draining NATS connection")
		_ = nc.Drain()
	}

	wg.Wait()
	slog.Info("email service stopped")
}

func setupNATSAndKV(ctx context.Context, natsURL string) (*natsgo.Conn, natsgo.JetStreamContext, natsgo.KeyValue, error) {
	nc, err := natsgo.Connect(
		natsURL,
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

	kv, err := js.KeyValue(api.EmailOpenTrackingKVBucket)
	if err != nil {
		slog.Warn("KV bucket not found, tracking disabled", "bucket", api.EmailOpenTrackingKVBucket, logging.ErrKey, err)
		return nc, js, nil, nil
	}

	return nc, js, kv, nil
}

func subscribeHandlers(ctx context.Context, nc *natsgo.Conn, sendEmailHandler *service.SendEmailHandler, kvStore natsgo.KeyValue, wg *sync.WaitGroup, done chan os.Signal) error {
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

	_, err := nc.QueueSubscribe(api.SendEmailSubject, api.QueueGroup, func(msg *natsgo.Msg) {
		sendEmailHandler.Handle(msgCtx, msg)
	})
	if err != nil {
		msgCancel()
		return fmt.Errorf("nats subscribe send_email: %w", err)
	}
	slog.Info("subscribed to NATS subject", "subject", api.SendEmailSubject, "queue", api.QueueGroup)

	if kvStore != nil {
		statusHandler := service.NewGetEmailStatusHandler(kvStore)
		_, err = nc.QueueSubscribe(api.GetEmailStatusSubject, api.QueueGroup, func(msg *natsgo.Msg) {
			statusHandler.Handle(msgCtx, msg)
		})
		if err != nil {
			msgCancel()
			return fmt.Errorf("nats subscribe get_email_status: %w", err)
		}
		slog.Info("subscribed to NATS subject", "subject", api.GetEmailStatusSubject, "queue", api.QueueGroup)
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
