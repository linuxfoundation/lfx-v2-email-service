// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
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
	kvinfra "github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/kv"
	natstracing "github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/nats"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/observability"
	smtpinfra "github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/smtp"
	sqsinfra "github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/sqs"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

const gracefulShutdownSeconds = 25

// maxInFlightReads bounds how many get_email_status and
// get_email_engagement_analytics requests one replica handles at once. These
// handlers run off the subscription goroutine so a slow group lookup does not
// hold up every other request on the subject; once all slots are taken,
// further requests are answered immediately with "service busy".
const maxInFlightReads = 8

// readDrainTimeout bounds how long shutdown waits for in-flight status and
// analytics requests. It covers the longest handler deadline (10s for
// analytics) plus one KV read already in flight when that deadline passes:
// nats.go's KeyValue.Get takes no context and uses the JetStream default
// request timeout of 5s. The wait is taken from the shared shutdown budget
// (gracefulShutdownSeconds), and the NATS drain gets whatever remains.
const readDrainTimeout = 16 * time.Second

func main() {
	logging.InitStructuredLogConfig()
	env := parseEnv()

	ctx, cancel := context.WithCancel(context.Background())

	otelShutdown, err := observability.SetupOTelSDK(ctx)
	if err != nil {
		slog.Error("failed to set up OTel SDK", logging.ErrKey, err)
		cancel()
		os.Exit(1)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		if err := otelShutdown(shutCtx); err != nil {
			slog.Error("OTel shutdown error", logging.ErrKey, err)
		}
	}()

	var sender domain.Sender
	if env.EmailEnabled {
		sender = smtpinfra.NewSMTPSender(smtpinfra.Config{
			Host:             env.SMTP.Host,
			Port:             env.SMTP.Port,
			From:             env.SMTP.From,
			FromDisplayName:  env.SMTP.FromDisplayName,
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

	nc, store, kvAvailable, err := setupNATSAndKV(ctx, env.NatsURL)
	if err != nil {
		slog.Error("failed to connect to NATS", logging.ErrKey, err)
		cancel()
		os.Exit(1) //nolint:gocritic // startup failure; deferred OTel flush skipped, no spans emitted yet
	}

	slog.Info("from address allowlist configured", "allowed_domains", env.SMTP.AllowedFromDomains)
	slog.Info("reply_to address allowlist configured", "allowed_domains", env.SMTP.AllowedReplyToDomains)
	slog.Info("recipient address allowlist configured", "allowed_domains", env.SMTP.AllowedRecipientDomains)

	addrPolicy := domain.NewAddressPolicy(env.SMTP.AllowedFromDomains, env.SMTP.AllowedReplyToDomains, env.SMTP.AllowedRecipientDomains)

	if !kvAvailable {
		slog.WarnContext(ctx, "NATS KV tracking unavailable: status and analytics handlers will respond with not-found")
	}

	wg.Add(2) // HTTP server + NATS drain
	drainReads, err := subscribeHandlers(ctx, nc, sender, store, addrPolicy, &wg, done)
	if err != nil {
		slog.Error("failed to subscribe NATS handlers", logging.ErrKey, err)
		cancel()
		os.Exit(1) //nolint:gocritic // startup failure; deferred OTel flush skipped, no spans emitted yet
	}

	var pollerAborted atomic.Bool

	if env.SESEventingEnabled {
		if env.SESEngagementSQSURL == "" {
			slog.Error("SES_EVENTING_ENABLED is true but SES_ENGAGEMENT_SQS_QUEUE_URL is not set")
			cancel()
			os.Exit(1) //nolint:gocritic // startup failure; deferred OTel flush skipped, no spans emitted yet
		}
		if !kvAvailable {
			slog.Error("SES_EVENTING_ENABLED is true but NATS KV (email-recipients bucket) is unavailable")
			cancel()
			os.Exit(1) //nolint:gocritic // startup failure; deferred OTel flush skipped, no spans emitted yet
		}
		awsCfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			slog.Error("failed to load AWS config for SQS poller", logging.ErrKey, err)
			cancel()
			os.Exit(1) //nolint:gocritic // startup failure; deferred OTel flush skipped, no spans emitted yet
		}
		sqsClient := awssqs.NewFromConfig(awsCfg)
		engagementHandler := service.NewEngagementEventHandler(store).WithEngagementPublisher(nc)
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
	// One deadline for the whole NATS shutdown: the read wait and the
	// connection drain together must fit the shutdown budget, which (with the
	// OTel flush) stays within the pod's default 30s termination grace period.
	shutdownDeadline := time.Now().Add(gracefulShutdownSeconds * time.Second)

	cancel()

	go func() {
		defer wg.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), gracefulShutdownSeconds*time.Second)
		defer shutCancel()
		_ = httpServer.Shutdown(shutCtx)
		slog.Info("HTTP server stopped")
	}()

	if !nc.IsClosed() && !nc.IsDraining() {
		// Status and analytics requests run on their own goroutines, which
		// nc.Drain does not wait for; finish them while replies can still be sent.
		drainReads(min(readDrainTimeout, time.Until(shutdownDeadline)))
		slog.Info("draining NATS connection")
		_ = nc.Drain()
		// nc.Drain's own DrainTimeout counts from now, after the read wait, so
		// close the connection at the shared deadline if the drain is still
		// running. Closing runs the closed handler, which releases wg.
		closeAtDeadline := time.AfterFunc(time.Until(shutdownDeadline), func() {
			if !nc.IsClosed() {
				slog.Warn("NATS drain did not finish by the shutdown deadline, closing connection")
				nc.Close()
			}
		})
		defer closeAtDeadline.Stop()
	}

	wg.Wait()
	slog.Info("email service stopped")
	if pollerAborted.Load() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		if err := otelShutdown(shutCtx); err != nil {
			slog.Error("OTel shutdown error", logging.ErrKey, err)
		}
		os.Exit(1) //nolint:gocritic // explicit OTel flush above; deferred shutdown is a no-op after flush
	}
}

func setupNATSAndKV(ctx context.Context, natsURL string) (*natsgo.Conn, domain.TrackingStore, bool, error) {
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
		return nil, nil, false, fmt.Errorf("nats connect: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		slog.Warn("JetStream not available, KV tracking disabled", logging.ErrKey, err)
		return nc, domain.NullTrackingStore{}, false, nil
	}

	recipientsKV, err := js.KeyValue(api.EmailRecipientsKVBucket)
	if err != nil {
		slog.Warn("KV bucket not found, tracking disabled", "bucket", api.EmailRecipientsKVBucket, logging.ErrKey, err)
		return nc, domain.NullTrackingStore{}, false, nil
	}

	groupIndexKV, err := js.KeyValue(api.EmailGroupIndexKVBucket)
	if err != nil {
		slog.Warn("KV bucket not found, tracking disabled", "bucket", api.EmailGroupIndexKVBucket, logging.ErrKey, err)
		return nc, domain.NullTrackingStore{}, false, nil
	}

	return nc, kvinfra.New(recipientsKV, groupIndexKV), true, nil
}

func subscribeHandlers(
	ctx context.Context,
	nc *natsgo.Conn,
	sender domain.Sender,
	store domain.TrackingStore,
	addrPolicy domain.AddressPolicy,
	wg *sync.WaitGroup,
	done chan os.Signal,
) (drainReads func(timeout time.Duration), err error) {
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

	sendHandler := service.NewSendEmailHandler(sender, store, addrPolicy)
	if _, err := nc.QueueSubscribe(api.SendEmailSubject, api.QueueGroup, func(msg *natsgo.Msg) {
		spanCtx, span := natstracing.ExtractAndStartConsumerSpan(msgCtx, msg, api.SendEmailSubject)
		defer span.End()
		sendHandler.Handle(spanCtx, msg)
	}); err != nil {
		msgCancel()
		return nil, fmt.Errorf("nats subscribe %s: %w", api.SendEmailSubject, err)
	}
	slog.Info("subscribed to NATS subject", "subject", api.SendEmailSubject, "queue", api.QueueGroup)

	// Each read request runs on its own goroutine while it holds one of the
	// maxInFlightReads slots; the handlers bound each request's own work with a
	// deadline. The consumer span is started here, in the subscription
	// callback, and ended by the goroutine.
	readSlots := make(chan struct{}, maxInFlightReads)
	busyReply, _ := json.Marshal(api.SendEmailErrorResponse{Error: "service busy"})
	dispatchRead := func(msg *natsgo.Msg, subject string, handle func(context.Context, *natsgo.Msg)) {
		spanCtx, span := natstracing.ExtractAndStartConsumerSpan(msgCtx, msg, subject)
		select {
		case readSlots <- struct{}{}:
		default:
			defer span.End()
			slog.WarnContext(spanCtx, "read request rejected, all read slots busy", "subject", subject)
			if err := msg.Respond(busyReply); err != nil {
				slog.WarnContext(spanCtx, "failed to respond with busy error to NATS request", logging.ErrKey, err)
			}
			return
		}
		go func() {
			defer func() { <-readSlots }()
			defer span.End()
			handle(spanCtx, msg)
		}()
	}

	statusHandler := service.NewGetEmailStatusHandler(store).WithMaxPayload(nc.MaxPayload())
	statusSub, err := nc.QueueSubscribe(api.GetEmailStatusSubject, api.QueueGroup, func(msg *natsgo.Msg) {
		dispatchRead(msg, api.GetEmailStatusSubject, statusHandler.Handle)
	})
	if err != nil {
		msgCancel()
		return nil, fmt.Errorf("nats subscribe %s: %w", api.GetEmailStatusSubject, err)
	}
	slog.Info("subscribed to NATS subject", "subject", api.GetEmailStatusSubject)

	analyticsHandler := service.NewGetEmailEngagementAnalyticsHandler(store)
	analyticsSub, err := nc.QueueSubscribe(api.GetEmailEngagementAnalyticsSubject, api.QueueGroup, func(msg *natsgo.Msg) {
		dispatchRead(msg, api.GetEmailEngagementAnalyticsSubject, analyticsHandler.Handle)
	})
	if err != nil {
		msgCancel()
		return nil, fmt.Errorf("nats subscribe %s: %w", api.GetEmailEngagementAnalyticsSubject, err)
	}
	slog.Info("subscribed to NATS subject", "subject", api.GetEmailEngagementAnalyticsSubject)

	// drainReads stops delivery on the read subscriptions, waits for their
	// callbacks to finish, then waits for every in-flight read goroutine by
	// taking all maxInFlightReads slots. Taking the slots (rather than a
	// WaitGroup) is safe against a late callback: it finds no free slot and
	// replies "service busy" while the connection is still open.
	drainReads = func(timeout time.Duration) {
		deadline := time.After(timeout)
		var closed []<-chan natsgo.SubStatus
		for _, sub := range []*natsgo.Subscription{statusSub, analyticsSub} {
			if !sub.IsValid() {
				continue
			}
			closed = append(closed, sub.StatusChanged(natsgo.SubscriptionClosed))
			if err := sub.Drain(); err != nil {
				slog.Warn("failed to drain read subscription", "subject", sub.Subject, logging.ErrKey, err)
			}
		}
		for _, ch := range closed {
			for open := true; open; {
				select {
				case _, open = <-ch: // closed once the subscription is closed
				case <-deadline:
					slog.Warn("timed out waiting for read subscriptions to drain")
					return
				}
			}
		}
		for range maxInFlightReads {
			select {
			case readSlots <- struct{}{}:
			case <-deadline:
				slog.Warn("timed out waiting for in-flight read requests")
				return
			}
		}
	}

	return drainReads, nil
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
