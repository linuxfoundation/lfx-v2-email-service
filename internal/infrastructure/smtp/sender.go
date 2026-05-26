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

// Send renders and delivers an email via SMTP.
// A 30-second deadline is applied to the blocking SMTP call.
// Returns the emailID (per-send UUID) and groupID (campaign UUID) assigned to this message.
func (s *SMTPSender) Send(ctx context.Context, req api.SendEmailRequest) (emailID, groupID string, err error) {
	emailID = uuid.NewString()
	groupID = req.GroupID
	if groupID == "" {
		groupID = uuid.NewString()
	}

	trackingID := groupID + "/" + emailID

	sendCtx, cancel := context.WithTimeout(ctx, smtpTimeout)
	defer cancel()

	msg := buildEmailMessage(req.To, req.Subject, req.HTML, req.Text, s.cfg.From, s.cfg.ConfigurationSet, trackingID)
	if err := sendMessage(sendCtx, req.To, msg, s.cfg); err != nil {
		return "", "", fmt.Errorf("smtp send: %w", err)
	}

	slog.DebugContext(ctx, "email sent", "email_id", emailID, "group_id", groupID)
	return emailID, groupID, nil
}
