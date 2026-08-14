// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// GetEmailStatusHandler handles NATS requests on the get_email_status subject.
type GetEmailStatusHandler struct {
	store domain.TrackingStore
}

// NewGetEmailStatusHandler creates a handler backed by store.
func NewGetEmailStatusHandler(store domain.TrackingStore) *GetEmailStatusHandler {
	return &GetEmailStatusHandler{store: store}
}

// Handle processes a single NATS message.
func (h *GetEmailStatusHandler) Handle(ctx context.Context, msg *natsgo.Msg) {
	h.HandleData(ctx, msg.Data, msg.Respond)
}

// HandleData is the testable core: respond is called exactly once.
func (h *GetEmailStatusHandler) HandleData(ctx context.Context, data []byte, respond func([]byte) error) {
	var req api.GetEmailStatusRequest
	if err := json.Unmarshal(data, &req); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal get_email_status request", logging.ErrKey, err)
		replyError(ctx, respond, "invalid request payload")
		return
	}

	switch {
	case req.EmailID != "" && req.GroupID != "":
		replyError(ctx, respond, "only one of email_id or group_id may be set")
	case req.EmailID != "":
		h.handleByEmailID(ctx, respond, req.EmailID)
	case req.GroupID != "":
		h.handleByGroupID(ctx, respond, req.GroupID)
	default:
		replyError(ctx, respond, "email_id or group_id is required")
	}
}

func (h *GetEmailStatusHandler) handleByEmailID(ctx context.Context, respond func([]byte) error, emailID string) {
	ctx = logging.AppendCtx(ctx, slog.String("email_id", emailID))
	record, err := h.store.GetRecord(ctx, emailID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			slog.DebugContext(ctx, "recipient record not found")
			replyError(ctx, respond, "not found")
		} else {
			slog.ErrorContext(ctx, "failed to read recipient record", logging.ErrKey, err)
			replyError(ctx, respond, "internal error")
		}
		return
	}

	b, err := json.Marshal(record)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal recipient record response", logging.ErrKey, err)
		replyError(ctx, respond, "internal error")
		return
	}
	if err := respond(b); err != nil {
		slog.WarnContext(ctx, "failed to respond to get_email_status request", logging.ErrKey, err)
	}
}

func (h *GetEmailStatusHandler) handleByGroupID(ctx context.Context, respond func([]byte) error, groupID string) {
	ctx = logging.AppendCtx(ctx, slog.String("group_id", groupID))
	records, _, err := h.store.GetGroupRecords(ctx, groupID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			slog.DebugContext(ctx, "group index not found")
			replyError(ctx, respond, "not found")
		} else {
			slog.ErrorContext(ctx, "failed to read group records", logging.ErrKey, err)
			replyError(ctx, respond, "internal error")
		}
		return
	}

	b, err := json.Marshal(records)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal group status response", logging.ErrKey, err)
		replyError(ctx, respond, "internal error")
		return
	}
	if err := respond(b); err != nil {
		slog.WarnContext(ctx, "failed to respond to get_email_status (group) request", logging.ErrKey, err)
	}
}
