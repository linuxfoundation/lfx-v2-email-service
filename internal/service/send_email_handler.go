// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package service contains the NATS message handlers for the email service.
package service

import (
	"context"
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

// SendEmailHandler handles inbound NATS requests on the send_email subject.
type SendEmailHandler struct {
	sender domain.Sender
	store  domain.TrackingStore
	policy domain.AddressPolicy
}

// NewSendEmailHandler creates a SendEmailHandler.
// store must not be nil; use domain.NullTrackingStore{} when tracking is unavailable.
// policy carries the three address allowlists; construct it with domain.NewAddressPolicy.
func NewSendEmailHandler(sender domain.Sender, store domain.TrackingStore, policy domain.AddressPolicy) *SendEmailHandler {
	return &SendEmailHandler{sender: sender, store: store, policy: policy}
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

	// Recipient domain allowlist. Empty = permit all (production default). Set in non-prod
	// to prevent test mail from reaching real users' personal addresses. A blocked recipient
	// returns an empty success response (not an error) so callers don't treat expected
	// non-prod filtering as a delivery failure.
	if len(h.policy.AllowedRecipientDomains) > 0 {
		allowed, err := h.policy.IsRecipientAllowed(req.To)
		if err != nil {
			slog.WarnContext(ctx, "send email request has malformed recipient address, skipping send",
				"to", redaction.RedactEmail(req.To), logging.ErrKey, err)
		} else if !allowed {
			slog.WarnContext(ctx, "send email request recipient domain not in allowlist, skipping send",
				"to", redaction.RedactEmail(req.To))
		}
		if !allowed {
			resp, _ := json.Marshal(api.SendEmailResponse{})
			if err := respond(resp); err != nil {
				slog.WarnContext(ctx, "failed to respond to NATS request", logging.ErrKey, err)
			}
			return
		}
	}

	// Validate per-message From override when provided.
	if req.From != "" {
		if err := h.policy.ValidateFrom(req.From); err != nil {
			if errors.Is(err, domain.ErrAddressMalformed) {
				slog.WarnContext(ctx, "send email request has invalid from address", "from", redaction.RedactEmail(req.From), logging.ErrKey, err)
				replyError(ctx, respond, "invalid from address")
			} else {
				parts := strings.SplitN(req.From, "@", 2)
				d := req.From
				if len(parts) == 2 {
					d = strings.ToLower(parts[1])
				}
				slog.WarnContext(ctx, "send email request from domain not in allowlist", "domain", d)
				replyError(ctx, respond, "from address domain not allowed")
			}
			return
		}
	}

	if req.ReplyTo != "" {
		if err := h.policy.ValidateReplyTo(req.ReplyTo); err != nil {
			if errors.Is(err, domain.ErrAddressMalformed) {
				slog.WarnContext(ctx, "send email request has invalid reply_to address", "reply_to", redaction.RedactEmail(req.ReplyTo), logging.ErrKey, err)
				replyError(ctx, respond, "invalid reply_to address")
			} else {
				parts := strings.SplitN(req.ReplyTo, "@", 2)
				d := req.ReplyTo
				if len(parts) == 2 {
					d = strings.ToLower(parts[1])
				}
				slog.WarnContext(ctx, "send email request reply_to domain not in allowlist", "domain", d)
				replyError(ctx, respond, "reply_to address domain not allowed")
			}
			return
		}
	}

	ctx = logging.AppendCtx(ctx, slog.String("recipient", redaction.RedactEmail(req.To)))
	ctx = logging.AppendCtx(ctx, slog.String("subject", req.Subject))

	emailID, groupID, err := h.sender.Send(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "email send failed", logging.ErrKey, err)
		replyError(ctx, respond, "email delivery failed")
		return
	}

	if emailID != "" {
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
	if err := h.store.WriteRecord(ctx, emailID, record); err != nil {
		slog.WarnContext(ctx, "failed to write recipient record to store", logging.ErrKey, err, "email_id", emailID)
		return
	}
	if groupID != "" {
		if err := h.store.AppendToGroup(ctx, groupID, emailID); err != nil {
			slog.WarnContext(ctx, "failed to append email to group index", logging.ErrKey, err, "email_id", emailID, "group_id", groupID)
		}
	}
}

func replyError(ctx context.Context, respond func([]byte) error, reason string) {
	body, _ := json.Marshal(api.SendEmailErrorResponse{Error: reason})
	if err := respond(body); err != nil {
		slog.WarnContext(ctx, "failed to respond with error to NATS request", logging.ErrKey, err)
	}
}
