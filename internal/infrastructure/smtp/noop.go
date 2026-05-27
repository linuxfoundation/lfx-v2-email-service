// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package smtp

import (
	"context"
	"log/slog"

	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/redaction"
)

// NoOpSender logs email requests without delivering them.
// Used when EMAIL_ENABLED=false (local dev or testing).
type NoOpSender struct{}

// NewNoOpSender creates a NoOpSender.
func NewNoOpSender() *NoOpSender { return &NoOpSender{} }

// Send logs the request and returns empty IDs without sending anything.
func (s *NoOpSender) Send(ctx context.Context, req api.SendEmailRequest) (string, string, error) {
	slog.InfoContext(ctx, "email send skipped (EMAIL_ENABLED=false)",
		"to", redaction.RedactEmail(req.To),
		"subject", req.Subject,
	)
	return "", "", nil
}
