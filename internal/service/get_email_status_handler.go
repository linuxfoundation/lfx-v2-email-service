// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/redaction"
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

	// group_id (the service-issued group handle) is the access credential for
	// every lookup. email_id alone is not: it is carried in the outbound
	// X-LFX-TRACKING-ID header and in push events, so it is not a secret.
	switch {
	case req.GroupID == "":
		replyError(ctx, respond, "group_id is required")
	case !isValidGroupID(req.GroupID):
		replyError(ctx, respond, "invalid group_id")
	case req.EmailID != "":
		if !isValidUUID(req.EmailID) {
			replyError(ctx, respond, "invalid email_id")
			return
		}
		h.handleByEmailID(ctx, respond, strings.ToLower(req.EmailID), req.GroupID)
	default:
		h.handleByGroupID(ctx, respond, req.GroupID)
	}
}

func (h *GetEmailStatusHandler) handleByEmailID(ctx context.Context, respond func([]byte) error, emailID, groupID string) {
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
	// The record is returned only to a caller holding the group handle it was
	// sent under. A mismatch replies "not found" so the reply does not reveal
	// whether the email_id exists.
	if subtle.ConstantTimeCompare([]byte(record.GroupID), []byte(groupID)) != 1 {
		slog.DebugContext(ctx, "recipient record does not belong to requested group")
		replyError(ctx, respond, "not found")
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
	ctx = logging.AppendCtx(ctx, slog.String("group_id", redaction.RedactGroupHandle(groupID)))
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
