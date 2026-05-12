// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package smtp implements email delivery via SMTP (Amazon SES or compatible).
package smtp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/redaction"
)

// Config holds SMTP connection parameters.
type Config struct {
	Host     string
	Port     int
	From     string
	Username string
	Password string
}

// SMTPSender sends emails via SMTP.
type SMTPSender struct {
	cfg Config
}

// NewSMTPSender creates a new SMTPSender with the given config.
func NewSMTPSender(cfg Config) *SMTPSender {
	return &SMTPSender{cfg: cfg}
}

// Send renders and delivers an email via SMTP.
func (s *SMTPSender) Send(ctx context.Context, req api.SendEmailRequest) error {
	ctx = logging.AppendCtx(ctx, slog.String("recipient", redaction.RedactEmail(req.To)))
	ctx = logging.AppendCtx(ctx, slog.String("subject", req.Subject))

	msg := buildEmailMessage(req.To, req.Subject, req.HTML, req.Text, s.cfg.From)
	if err := sendMessage(req.To, msg, s.cfg); err != nil {
		slog.ErrorContext(ctx, "failed to send email", logging.ErrKey, err)
		return fmt.Errorf("smtp send: %w", err)
	}

	slog.DebugContext(ctx, "email sent successfully")
	return nil
}
