// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package service contains the NATS message handlers for the email service.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/redaction"
)

// SendEmailHandler handles inbound NATS requests on the send_email subject.
type SendEmailHandler struct {
	sender       domain.Sender
	recipientsKV natsgo.KeyValue
	groupIndexKV natsgo.KeyValue
}

// NewSendEmailHandler creates a SendEmailHandler.
// recipientsKV and groupIndexKV may be nil; tracking is skipped when either is absent.
func NewSendEmailHandler(sender domain.Sender, recipientsKV, groupIndexKV natsgo.KeyValue) *SendEmailHandler {
	return &SendEmailHandler{sender: sender, recipientsKV: recipientsKV, groupIndexKV: groupIndexKV}
}

// Handle processes a single NATS message.
func (h *SendEmailHandler) Handle(ctx context.Context, msg *natsgo.Msg) {
	h.HandleData(ctx, msg.Data, msg.Respond)
}

// HandleData is the testable core: respond is called exactly once.
func (h *SendEmailHandler) HandleData(ctx context.Context, data []byte, respond func([]byte) error) {
	var req api.SendEmailRequest
	if err := json.Unmarshal(data, &req); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal send email request", logging.ErrKey, err)
		replyError(ctx, respond, "invalid request payload")
		return
	}

	if req.To == "" || req.Subject == "" || req.HTML == "" || req.Text == "" {
		slog.WarnContext(ctx, "send email request missing required fields",
			"has_to", req.To != "",
			"has_subject", req.Subject != "",
			"has_html", req.HTML != "",
			"has_text", req.Text != "",
		)
		replyError(ctx, respond, "to, subject, html, and text are required")
		return
	}

	ctx = logging.AppendCtx(ctx, slog.String("recipient", redaction.RedactEmail(req.To)))
	ctx = logging.AppendCtx(ctx, slog.String("subject", req.Subject))

	emailID, groupID, err := h.sender.Send(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "email send failed", logging.ErrKey, err)
		replyError(ctx, respond, "email delivery failed")
		return
	}

	if emailID != "" && h.recipientsKV != nil && h.groupIndexKV != nil {
		h.writeTrackingRecords(ctx, emailID, groupID, req)
	}

	resp, _ := json.Marshal(api.SendEmailResponse{EmailID: emailID, GroupID: groupID})
	if err := respond(resp); err != nil {
		slog.WarnContext(ctx, "failed to respond to NATS request", logging.ErrKey, err)
	}
}

func (h *SendEmailHandler) writeTrackingRecords(ctx context.Context, emailID, groupID string, req api.SendEmailRequest) {
	record := api.EmailRecipientRecord{
		GroupID: groupID,
		EmailID: emailID,
		To:      req.To,
		Subject: req.Subject,
		SentAt:  time.Now().UTC(),
	}
	b, err := json.Marshal(record)
	if err != nil {
		slog.WarnContext(ctx, "failed to marshal recipient record", logging.ErrKey, err)
		return
	}
	if _, err := h.recipientsKV.Put(emailID, b); err != nil {
		slog.WarnContext(ctx, "failed to write recipient record to KV", logging.ErrKey, err, "email_id", emailID)
	}

	if groupID != "" {
		h.appendToGroupIndex(ctx, groupID, emailID)
	}
}

// appendToGroupIndex adds emailID to the group's index entry with optimistic locking.
// Retries once on write conflict. Distinguishes ErrKeyNotFound from transient errors
// so a Get failure does not silently overwrite the existing index.
func (h *SendEmailHandler) appendToGroupIndex(ctx context.Context, groupID, emailID string) {
	for attempt := range 2 {
		var ids []string
		var revision uint64
		var isNew bool

		entry, err := h.groupIndexKV.Get(groupID)
		switch {
		case err == nil:
			revision = entry.Revision()
			if jsonErr := json.Unmarshal(entry.Value(), &ids); jsonErr != nil {
				slog.WarnContext(ctx, "corrupted group index, resetting", "group_id", groupID, logging.ErrKey, jsonErr)
				ids = nil
			}
		case errors.Is(err, natsgo.ErrKeyNotFound):
			isNew = true
		default:
			slog.WarnContext(ctx, "failed to read group index, aborting append", "group_id", groupID, logging.ErrKey, err)
			return
		}

		ids = append(ids, emailID)
		b, _ := json.Marshal(ids)

		var writeErr error
		if isNew {
			_, writeErr = h.groupIndexKV.Put(groupID, b)
		} else {
			_, writeErr = h.groupIndexKV.Update(groupID, b, revision)
		}

		if writeErr == nil {
			return
		}
		if attempt == 0 {
			slog.DebugContext(ctx, "group index write conflict, retrying", "group_id", groupID)
		}
	}
	slog.WarnContext(ctx, "failed to update group index after retry", "group_id", groupID)
}

func replyError(ctx context.Context, respond func([]byte) error, reason string) {
	body, _ := json.Marshal(api.SendEmailErrorResponse{Error: reason})
	if err := respond(body); err != nil {
		slog.WarnContext(ctx, "failed to respond with error to NATS request", logging.ErrKey, err)
	}
}
