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
	kvStore natsgo.KeyValue
}

// NewGetEmailStatusHandler creates a handler backed by kvStore.
func NewGetEmailStatusHandler(kvStore natsgo.KeyValue) *GetEmailStatusHandler {
	return &GetEmailStatusHandler{kvStore: kvStore}
}

// Handle processes a single NATS message.
func (h *GetEmailStatusHandler) Handle(ctx context.Context, msg *natsgo.Msg) {
	var req api.GetEmailStatusRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal get_email_status request", logging.ErrKey, err)
		respondErrorMsg(msg, "invalid request payload")
		return
	}

	if req.SESMessageID == "" && req.CorrelationID == "" {
		respondErrorMsg(msg, "ses_message_id or correlation_id is required")
		return
	}

	var record *api.EmailTrackingRecord

	if req.SESMessageID != "" {
		r, err := h.lookupByMessageID(ctx, req.SESMessageID)
		if err != nil {
			respondErrorMsg(msg, "not found")
			return
		}
		record = r
	} else {
		r, err := h.lookupByCorrelationID(ctx, req.CorrelationID)
		if err != nil {
			respondErrorMsg(msg, "not found")
			return
		}
		record = r
	}

	b, err := json.Marshal(record)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal tracking record", logging.ErrKey, err)
		respondErrorMsg(msg, "internal error")
		return
	}
	if err := msg.Respond(b); err != nil {
		slog.WarnContext(ctx, "failed to respond to get_email_status request", logging.ErrKey, err)
	}
}

func (h *GetEmailStatusHandler) lookupByMessageID(ctx context.Context, messageID string) (*api.EmailTrackingRecord, error) {
	key := "ses-message-id/" + messageID
	entry, err := h.kvStore.Get(key)
	if err != nil {
		slog.DebugContext(ctx, "tracking record not found", "key", key)
		return nil, err
	}
	var record api.EmailTrackingRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (h *GetEmailStatusHandler) lookupByCorrelationID(ctx context.Context, correlationID string) (*api.EmailTrackingRecord, error) {
	keys, err := h.kvStore.Keys()
	if err != nil {
		slog.WarnContext(ctx, "failed to list kv keys for correlation id lookup", logging.ErrKey, err)
		return nil, err
	}
	for _, key := range keys {
		entry, err := h.kvStore.Get(key)
		if err != nil {
			continue
		}
		var record api.EmailTrackingRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			continue
		}
		if record.CorrelationID == correlationID {
			return &record, nil
		}
	}
	return nil, natsgo.ErrKeyNotFound
}

func respondErrorMsg(msg *natsgo.Msg, reason string) {
	body, _ := json.Marshal(api.SendEmailErrorResponse{Error: reason})
	_ = msg.Respond(body)
}
