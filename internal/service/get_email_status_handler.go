// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/redaction"
)

// groupReadTimeout bounds the time one status or analytics request may spend
// resolving a group's records. The NATS subscription callbacks carry no
// deadline of their own, so the handlers apply this one.
const groupReadTimeout = 5 * time.Second

// defaultMaxPayload is the NATS server's default max_payload, used when the
// handler is not told the connection's actual limit.
const defaultMaxPayload int64 = 1 << 20

// GetEmailStatusHandler handles NATS requests on the get_email_status subject.
type GetEmailStatusHandler struct {
	store      domain.TrackingStore
	maxPayload int64
}

// NewGetEmailStatusHandler creates a handler backed by store.
func NewGetEmailStatusHandler(store domain.TrackingStore) *GetEmailStatusHandler {
	return &GetEmailStatusHandler{store: store, maxPayload: defaultMaxPayload}
}

// WithMaxPayload sets the largest reply, in bytes, the handler will send
// (the connection's max payload). A group page that would exceed it is
// answered with "response too large" instead of a reply NATS would reject.
// Values <= 0 are ignored.
func (h *GetEmailStatusHandler) WithMaxPayload(n int64) *GetEmailStatusHandler {
	if n > 0 {
		h.maxPayload = n
	}
	return h
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
	case req.Offset < 0:
		replyError(ctx, respond, "invalid offset")
	case req.Limit < 0 || req.Limit > api.MaxGroupStatusLimit:
		replyError(ctx, respond, "invalid limit")
	default:
		limit := req.Limit
		if limit == 0 {
			limit = api.DefaultGroupStatusLimit
		}
		h.handleByGroupID(ctx, respond, req.GroupID, req.Offset, limit)
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

func (h *GetEmailStatusHandler) handleByGroupID(ctx context.Context, respond func([]byte) error, groupID string, offset, limit int) {
	ctx = logging.AppendCtx(ctx, slog.String("group_id", redaction.RedactGroupHandle(groupID)))
	ctx, cancel := context.WithTimeout(ctx, groupReadTimeout)
	defer cancel()

	// The reply is built incrementally so its size is known before it is sent:
	// a page that would exceed the connection's max payload stops the scan and
	// gets an explicit error reply rather than a reply NATS would refuse.
	buf := bytes.NewBufferString("[")
	tooLarge := false
	var marshalErr error
	_, err := h.store.ScanGroupRecords(ctx, groupID, offset, limit, func(r api.EmailRecipientRecord) bool {
		b, err := json.Marshal(r)
		if err != nil {
			marshalErr = err
			return false
		}
		sep := 0
		if buf.Len() > 1 {
			sep = 1 // ',' before every record but the first
		}
		// +1 for the closing bracket.
		if int64(buf.Len()+sep+len(b)+1) > h.maxPayload {
			tooLarge = true
			return false
		}
		if sep == 1 {
			buf.WriteByte(',')
		}
		buf.Write(b)
		return true
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		slog.DebugContext(ctx, "group index not found")
		replyError(ctx, respond, "not found")
		return
	case errors.Is(err, context.DeadlineExceeded):
		slog.WarnContext(ctx, "group status lookup timed out", "offset", offset, "limit", limit)
		replyError(ctx, respond, "timeout")
		return
	case err != nil:
		slog.ErrorContext(ctx, "failed to read group records", logging.ErrKey, err)
		replyError(ctx, respond, "internal error")
		return
	case marshalErr != nil:
		slog.ErrorContext(ctx, "failed to marshal group status response", logging.ErrKey, marshalErr)
		replyError(ctx, respond, "internal error")
		return
	case tooLarge:
		slog.WarnContext(ctx, "group status page exceeds max payload", "offset", offset, "limit", limit, "max_payload", h.maxPayload)
		replyError(ctx, respond, "response too large")
		return
	}
	buf.WriteByte(']')

	if err := respond(buf.Bytes()); err != nil {
		slog.WarnContext(ctx, "failed to respond to get_email_status (group) request", logging.ErrKey, err)
	}
}
