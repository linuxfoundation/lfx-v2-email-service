// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// GetEmailStatusHandler handles NATS requests on the get_email_status subject.
type GetEmailStatusHandler struct {
	recipientsKV natsgo.KeyValue
	groupIndexKV natsgo.KeyValue
}

// NewGetEmailStatusHandler creates a handler backed by recipientsKV and groupIndexKV.
func NewGetEmailStatusHandler(recipientsKV, groupIndexKV natsgo.KeyValue) *GetEmailStatusHandler {
	return &GetEmailStatusHandler{recipientsKV: recipientsKV, groupIndexKV: groupIndexKV}
}

// Handle processes a single NATS message.
func (h *GetEmailStatusHandler) Handle(ctx context.Context, msg *natsgo.Msg) {
	var req api.GetEmailStatusRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal get_email_status request", logging.ErrKey, err)
		respondErrorMsg(msg, "invalid request payload")
		return
	}

	switch {
	case req.EmailID != "" && req.GroupID != "":
		respondErrorMsg(msg, "only one of email_id or group_id may be set")
		return
	case req.EmailID != "":
		h.handleByEmailID(ctx, msg, req.EmailID)
	case req.GroupID != "":
		h.handleByGroupID(ctx, msg, req.GroupID)
	default:
		respondErrorMsg(msg, "email_id or group_id is required")
	}
}

func (h *GetEmailStatusHandler) handleByEmailID(ctx context.Context, msg *natsgo.Msg, emailID string) {
	entry, err := h.recipientsKV.Get(emailID)
	if err != nil {
		if errors.Is(err, natsgo.ErrKeyNotFound) {
			slog.DebugContext(ctx, "recipient record not found", "email_id", emailID)
			respondErrorMsg(msg, "not found")
		} else {
			slog.ErrorContext(ctx, "failed to read recipient record from KV", logging.ErrKey, err, "email_id", emailID)
			respondErrorMsg(msg, "internal error")
		}
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

func (h *GetEmailStatusHandler) handleByGroupID(ctx context.Context, msg *natsgo.Msg, groupID string) {
	entry, err := h.groupIndexKV.Get(groupID)
	if err != nil {
		if errors.Is(err, natsgo.ErrKeyNotFound) {
			slog.DebugContext(ctx, "group index not found", "group_id", groupID)
			respondErrorMsg(msg, "not found")
		} else {
			slog.ErrorContext(ctx, "failed to read group index from KV", logging.ErrKey, err, "group_id", groupID)
			respondErrorMsg(msg, "internal error")
		}
		return
	}

	var emailIDs []string
	if err := json.Unmarshal(entry.Value(), &emailIDs); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal group index", logging.ErrKey, err)
		respondErrorMsg(msg, "internal error")
		return
	}

	records := make([]api.EmailRecipientRecord, 0, len(emailIDs))
	for _, emailID := range emailIDs {
		recEntry, err := h.recipientsKV.Get(emailID)
		if err != nil {
			slog.DebugContext(ctx, "recipient record not found during group status lookup", "email_id", emailID)
			continue
		}
		var record api.EmailRecipientRecord
		if err := json.Unmarshal(recEntry.Value(), &record); err != nil {
			slog.WarnContext(ctx, "failed to unmarshal recipient record during group status lookup", logging.ErrKey, err, "email_id", emailID)
			continue
		}
		records = append(records, record)
	}

	resp := api.GetGroupEmailStatusResponse{
		GroupID: groupID,
		Emails:  records,
	}
	b, _ := json.Marshal(resp)
	if err := msg.Respond(b); err != nil {
		slog.WarnContext(ctx, "failed to respond to get_email_status (group) request", logging.ErrKey, err)
	}
}

func respondErrorMsg(msg *natsgo.Msg, reason string) {
	body, _ := json.Marshal(api.SendEmailErrorResponse{Error: reason})
	_ = msg.Respond(body)
}
