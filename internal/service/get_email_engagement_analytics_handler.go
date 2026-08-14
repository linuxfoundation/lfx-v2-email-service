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

// GetEmailEngagementAnalyticsHandler handles requests on the get_email_engagement_analytics subject.
type GetEmailEngagementAnalyticsHandler struct {
	store domain.TrackingStore
}

// NewGetEmailEngagementAnalyticsHandler creates the handler.
func NewGetEmailEngagementAnalyticsHandler(store domain.TrackingStore) *GetEmailEngagementAnalyticsHandler {
	return &GetEmailEngagementAnalyticsHandler{store: store}
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

	records, totalIDs, err := h.store.GetGroupRecords(ctx, req.GroupID)
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

	resp := api.GetEmailEngagementAnalyticsResponse{
		GroupID:   req.GroupID,
		TotalSent: totalIDs,
	}
	for _, record := range records {
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
