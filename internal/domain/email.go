// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package domain contains the internal interfaces for the email service.
package domain

import (
	"context"

	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// Sender is the interface implemented by SMTPSender and NoOpSender.
// emailID is the UUID assigned to this specific send (key in email-recipients KV).
// groupID is the UUID grouping this send with others of the same campaign.
// Both are empty strings when email sending is disabled (NoOpSender).
type Sender interface {
	Send(ctx context.Context, req api.SendEmailRequest) (emailID, groupID string, err error)
}
