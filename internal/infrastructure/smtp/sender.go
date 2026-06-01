// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package smtp implements email delivery via SMTP (Amazon SES or compatible).
package smtp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

const smtpTimeout = 30 * time.Second

// Config holds SMTP connection parameters.
type Config struct {
	Host             string
	Port             int
	From             string
	FromDisplayName  string // display name for the From header; defaults to "LFX Self Serve" when empty
	Username         string
	Password         string
	ConfigurationSet string
}

// SMTPSender sends emails via SMTP.
type SMTPSender struct {
	cfg Config
}

// NewSMTPSender creates a new SMTPSender with the given config.
func NewSMTPSender(cfg Config) *SMTPSender {
	return &SMTPSender{cfg: cfg}
}

const defaultFromDisplayName = "LFX Self Serve"

// Send renders and delivers an email via SMTP.
// A 30-second deadline is applied to the blocking SMTP call.
// Returns the emailID (per-send UUID) and groupID (campaign UUID) assigned to this message.
//
// req.From overrides the service-level DEFAULT_SMTP_FROM default. req.FromDisplayName overrides
// the display name shown in the From header (defaults to DEFAULT_SMTP_FROM_DISPLAY_NAME, itself
// defaulting to "LFX Self Serve"). Domain allowlist validation is performed upstream by
// the NATS handler before Send is called.
func (s *SMTPSender) Send(ctx context.Context, req api.SendEmailRequest) (emailID, groupID string, err error) {
	emailID = uuid.NewString()
	groupID = req.GroupID
	if groupID == "" {
		groupID = uuid.NewString()
	}

	trackingID := groupID + "/" + emailID

	// Resolve effective FROM values: per-message values take priority over service defaults.
	fromAddr := req.From
	if fromAddr == "" {
		fromAddr = s.cfg.From
	}
	fromDisplayName := req.FromDisplayName
	if fromDisplayName == "" {
		fromDisplayName = s.cfg.FromDisplayName
		if fromDisplayName == "" {
			fromDisplayName = defaultFromDisplayName
		}
	}

	sendCtx, cancel := context.WithTimeout(ctx, smtpTimeout)
	defer cancel()

	msg := buildEmailMessage(req.To, req.Subject, req.HTML, req.Text, fromAddr, fromDisplayName, s.cfg.ConfigurationSet, trackingID)
	if err := sendMessage(sendCtx, req.To, fromAddr, msg, s.cfg); err != nil {
		return "", "", fmt.Errorf("smtp send: %w", err)
	}

	slog.DebugContext(ctx, "email sent", "email_id", emailID, "group_id", groupID)
	return emailID, groupID, nil
}
