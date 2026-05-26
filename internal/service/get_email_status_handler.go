// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// GetEmailStatusHandler handles NATS requests on the get_email_status subject.
type GetEmailStatusHandler struct {
	recipientsKV natsgo.KeyValue
}

// NewGetEmailStatusHandler creates a handler backed by recipientsKV.
func NewGetEmailStatusHandler(recipientsKV natsgo.KeyValue) *GetEmailStatusHandler {
	return &GetEmailStatusHandler{recipientsKV: recipientsKV}
}

// Handle processes a single NATS message.
func (h *GetEmailStatusHandler) Handle(ctx context.Context, msg *natsgo.Msg) {
	var req api.GetEmailStatusRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal get_email_status request", logging.ErrKey, err)
		respondErrorMsg(msg, "invalid request payload")
		return
	}

	if req.EmailID == "" {
		respondErrorMsg(msg, "email_id is required")
		return
	}

	entry, err := h.recipientsKV.Get(req.EmailID)
	if err != nil {
		slog.DebugContext(ctx, "recipient record not found", "email_id", req.EmailID)
		respondErrorMsg(msg, "not found")
		return
	}

	var record api.EmailRecipientRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal recipient record", logging.ErrKey, err)
		respondErrorMsg(msg, "internal error")
		return
	}

	b, _ := json.Marshal(record)
	if err := msg.Respond(b); err != nil {
		slog.WarnContext(ctx, "failed to respond to get_email_status request", logging.ErrKey, err)
	}
}

func respondErrorMsg(msg *natsgo.Msg, reason string) {
	body, _ := json.Marshal(api.SendEmailErrorResponse{Error: reason})
	_ = msg.Respond(body)
}
