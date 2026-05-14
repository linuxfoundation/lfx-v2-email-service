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

	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
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
			Host:     env.SMTP.Host,
			Port:     env.SMTP.Port,
			From:     env.SMTP.From,
			Username: env.SMTP.Username,
			Password: env.SMTP.Password,
		})
		slog.Info("email sender ready", "smtp_host", env.SMTP.Host, "smtp_port", env.SMTP.Port)
	} else {
		sender = smtpinfra.NewNoOpSender()
		slog.Info("email sending disabled (EMAIL_ENABLED=false)")
	}

	sendEmailHandler := service.NewSendEmailHandler(sender)

	// Connect to NATS.
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(2) // HTTP server + NATS drain

	nc, err := setupNATS(ctx, env.NatsURL, sendEmailHandler, &wg, done)
	if err != nil {
		slog.Error("failed to connect to NATS", logging.ErrKey, err)
		cancel()
		os.Exit(1)
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

func setupNATS(ctx context.Context, natsURL string, handler *service.SendEmailHandler, wg *sync.WaitGroup, done chan os.Signal) (*natsgo.Conn, error) {
	// msgCtx is a separate context for in-flight message handling. It is not
	// cancelled until the NATS connection closes (after drain), so messages
	// that arrive during the drain window are still processed fully.
	msgCtx, msgCancel := context.WithCancel(context.Background())

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
		natsgo.ClosedHandler(func(_ *natsgo.Conn) {
			msgCancel()
			if ctx.Err() == nil {
				slog.Error("NATS connection closed unexpectedly")
				select {
				case done <- syscall.SIGTERM:
				default:
				}
			}
			wg.Done()
		}),
	)
	if err != nil {
		msgCancel()
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	_, err = nc.QueueSubscribe(api.SendEmailSubject, api.QueueGroup, func(msg *natsgo.Msg) {
		handler.Handle(msgCtx, msg)
	})
	if err != nil {
		return nil, fmt.Errorf("nats subscribe: %w", err)
	}

	slog.Info("subscribed to NATS subject", "subject", api.SendEmailSubject, "queue", api.QueueGroup)
	return nc, nil
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
