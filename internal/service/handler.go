// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package service contains the NATS message handlers for the email service.
package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/redaction"
)

// SendEmailHandler handles inbound NATS requests on the lfx.email-service.send_email subject.
type SendEmailHandler struct {
	sender domain.Sender
}

// NewSendEmailHandler creates a SendEmailHandler backed by the given Sender.
func NewSendEmailHandler(sender domain.Sender) *SendEmailHandler {
	return &SendEmailHandler{sender: sender}
}

// Handle processes a single NATS message. It always calls msg.Respond so the
// caller's RequestWithContext does not time out.
func (h *SendEmailHandler) Handle(ctx context.Context, msg *nats.Msg) {
	h.HandleData(ctx, msg.Data, msg.Respond)
}

// HandleData is the core handler logic. respond is called exactly once with either
// nil (success) or a JSON-encoded SendEmailErrorResponse (failure).
// Separating this from Handle makes the logic unit-testable without a real NATS connection.
func (h *SendEmailHandler) HandleData(ctx context.Context, data []byte, respond func([]byte) error) {
	var req api.SendEmailRequest
	if err := json.Unmarshal(data, &req); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal send email request", logging.ErrKey, err)
		replyError(respond, "invalid request payload")
		return
	}

	if req.To == "" || req.Subject == "" {
		slog.WarnContext(ctx, "send email request missing required fields",
			"has_to", req.To != "",
			"has_subject", req.Subject != "",
		)
		replyError(respond, "to and subject are required")
		return
	}

	ctx = logging.AppendCtx(ctx, slog.String("recipient", redaction.RedactEmail(req.To)))
	ctx = logging.AppendCtx(ctx, slog.String("subject", req.Subject))

	if err := h.sender.Send(ctx, req); err != nil {
		slog.ErrorContext(ctx, "email send failed", logging.ErrKey, err)
		replyError(respond, "email delivery failed")
		return
	}

	_ = respond(nil)
}

func replyError(respond func([]byte) error, reason string) {
	body, _ := json.Marshal(api.SendEmailErrorResponse{Error: reason})
	_ = respond(body)
}
