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

// GetEmailEngagementAnalyticsHandler handles requests on the get_email_engagement_analytics subject.
type GetEmailEngagementAnalyticsHandler struct {
	recipientsKV natsgo.KeyValue
	groupIndexKV natsgo.KeyValue
}

// NewGetEmailEngagementAnalyticsHandler creates the handler.
func NewGetEmailEngagementAnalyticsHandler(recipientsKV, groupIndexKV natsgo.KeyValue) *GetEmailEngagementAnalyticsHandler {
	return &GetEmailEngagementAnalyticsHandler{recipientsKV: recipientsKV, groupIndexKV: groupIndexKV}
}

// Handle processes a single NATS message.
func (h *GetEmailEngagementAnalyticsHandler) Handle(ctx context.Context, msg *natsgo.Msg) {
	h.HandleData(ctx, msg.Data, msg.Respond)
}

// HandleData is the testable core: respond is called exactly once.
func (h *GetEmailEngagementAnalyticsHandler) HandleData(ctx context.Context, data []byte, respond func([]byte) error) {
	var req api.GetEmailEngagementAnalyticsRequest
	if err := json.Unmarshal(data, &req); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal get_email_engagement_analytics request", logging.ErrKey, err)
		replyError(ctx, respond, "invalid request payload")
		return
	}

	if req.GroupID == "" {
		replyError(ctx, respond, "group_id is required")
		return
	}

	ctx = logging.AppendCtx(ctx, slog.String("group_id", req.GroupID))

	entry, err := h.groupIndexKV.Get(req.GroupID)
	if err != nil {
		if errors.Is(err, natsgo.ErrKeyNotFound) {
			slog.DebugContext(ctx, "group index not found")
			replyError(ctx, respond, "not found")
		} else {
			slog.ErrorContext(ctx, "failed to read group index from KV", logging.ErrKey, err)
			replyError(ctx, respond, "internal error")
		}
		return
	}

	var emailIDs []string
	if err := json.Unmarshal(entry.Value(), &emailIDs); err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal group index", logging.ErrKey, err)
		replyError(ctx, respond, "internal error")
		return
	}

	resp := api.GetEmailEngagementAnalyticsResponse{
		GroupID:   req.GroupID,
		TotalSent: len(emailIDs),
	}

	for _, emailID := range emailIDs {
		recEntry, err := h.recipientsKV.Get(emailID)
		if err != nil {
			slog.DebugContext(ctx, "recipient record not found during analytics", "email_id", emailID)
			continue
		}
		var record api.EmailRecipientRecord
		if err := json.Unmarshal(recEntry.Value(), &record); err != nil {
			continue
		}
		if record.Delivered {
			resp.Delivered++
		}
		resp.Opened += record.OpenCount
		if record.Opened {
			resp.UniqueOpened++
		}
		if record.Failed {
			resp.Failed++
		}
	}

	b, _ := json.Marshal(resp)
	if err := respond(b); err != nil {
		slog.WarnContext(ctx, "failed to respond to get_email_engagement_analytics request", logging.ErrKey, err)
	}
}
