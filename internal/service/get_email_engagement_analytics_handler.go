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
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/redaction"
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

	if !isValidGroupID(req.GroupID) {
		replyError(ctx, respond, "invalid group_id")
		return
	}

	ctx = logging.AppendCtx(ctx, slog.String("group_id", redaction.RedactGroupHandle(req.GroupID)))

	ctx, cancel := context.WithTimeout(ctx, analyticsReadTimeout)
	defer cancel()

	// Aggregate while scanning so no more than one chunk of records is held in
	// memory (plus the decoded index); the scan resolves at most
	// api.MaxGroupEmails entries.
	resp := api.GetEmailEngagementAnalyticsResponse{GroupID: req.GroupID}
	totalIDs, err := h.store.ScanGroupRecords(ctx, req.GroupID, 0, api.MaxGroupEmails, func(record api.EmailRecipientRecord) bool {
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
		return true
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			slog.DebugContext(ctx, "group index not found")
			replyError(ctx, respond, "not found")
		case errors.Is(err, context.DeadlineExceeded):
			slog.WarnContext(ctx, "group analytics lookup timed out")
			replyError(ctx, respond, "timeout")
		case errors.Is(err, context.Canceled):
			slog.WarnContext(ctx, "group analytics lookup canceled")
			replyError(ctx, respond, "internal error")
		default:
			slog.ErrorContext(ctx, "failed to read group records", logging.ErrKey, err)
			replyError(ctx, respond, "internal error")
		}
		return
	}
	resp.TotalSent = totalIDs

	b, _ := json.Marshal(resp)
	if err := respond(b); err != nil {
		slog.WarnContext(ctx, "failed to respond to get_email_engagement_analytics request", logging.ErrKey, err)
	}
}
