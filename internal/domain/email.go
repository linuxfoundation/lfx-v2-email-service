// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package domain contains the internal interfaces for the email service.
package domain

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// Sender is the interface implemented by SMTPSender and NoOpSender.
// The returned string is the Message-ID (without angle brackets) assigned to the sent email;
// it is the key used to look up tracking records in the NATS KV bucket.
// An empty string is returned when email sending is disabled (NoOpSender).
type Sender interface {
	Send(ctx context.Context, req api.SendEmailRequest) (messageID string, err error)
}
