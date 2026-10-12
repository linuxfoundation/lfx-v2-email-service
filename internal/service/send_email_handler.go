// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package service contains the NATS message handlers for the email service.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/mail"
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

	// Bound header and envelope fields before any of them is parsed or sent.
	if reason := oversizedFieldReason(req); reason != "" {
		slog.WarnContext(ctx, "send email request field exceeds length limit", "reason", reason)
		replyError(ctx, respond, reason)
		return
	}

	if req.GroupID != "" && !isValidGroupID(req.GroupID) {
		slog.WarnContext(ctx, "send email request has invalid group_id format")
		replyError(ctx, respond, "invalid group_id")
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
				slog.WarnContext(ctx, "send email request from domain not in allowlist", "domain", domainFromAddress(req.From))
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
				slog.WarnContext(ctx, "send email request reply_to domain not in allowlist", "domain", domainFromAddress(req.ReplyTo))
				replyError(ctx, respond, "reply_to address domain not allowed")
			}
			return
		}
	}

	// A caller-supplied group_id must name a group this service issued (returned
	// in an earlier SendEmailResponse) and recorded. Checking the format alone
	// would let a caller pick its own handle value; checking existence means only
	// a holder of an issued handle can append further sends to that group.
	if req.GroupID != "" {
		exists, err := h.store.GroupExists(ctx, req.GroupID)
		if errors.Is(err, domain.ErrGroupFull) {
			// Rejected before sending: the email could not be tracked in this group.
			slog.WarnContext(ctx, "send email request group_id is full", "group_id", redaction.RedactGroupHandle(req.GroupID))
			replyError(ctx, respond, "group is full")
			return
		}
		if err != nil {
			slog.ErrorContext(ctx, "failed to check group index", logging.ErrKey, err)
			replyError(ctx, respond, "internal error")
			return
		}
		if !exists {
			slog.WarnContext(ctx, "send email request group_id was not issued by this service")
			replyError(ctx, respond, "invalid group_id")
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

	if emailID != "" && !h.writeTrackingRecords(ctx, emailID, groupID, req.GroupID == "", req) {
		// The email was not recorded in the group: either a newly issued group
		// was never recorded (its handle would be rejected by later sends and
		// lookups), or the group filled up between the check above and the
		// append. Do not return a handle the email is not tracked under.
		groupID = ""
	}

	resp, _ := json.Marshal(api.SendEmailResponse{EmailID: emailID, GroupID: groupID})
	if err := respond(resp); err != nil {
		slog.WarnContext(ctx, "failed to respond to NATS request", logging.ErrKey, err)
	}
}

// writeTrackingRecords appends the email to the group index and stores the
// recipient record. Both writes are best-effort. The group index is written
// first: for a newly issued group it is what records the handle, and if it
// fails the handle is not returned, so no record is written that only that
// handle could reach. It reports false when a new group was not recorded, or
// when the group was already full (domain.ErrGroupFull); no record is written
// in either case.
func (h *SendEmailHandler) writeTrackingRecords(ctx context.Context, emailID, groupID string, newGroup bool, req api.SendEmailRequest) bool {
	if groupID != "" {
		if err := h.store.AppendToGroup(ctx, groupID, emailID); err != nil {
			switch {
			case errors.Is(err, domain.ErrTrackingUnavailable):
				slog.DebugContext(ctx, "tracking unavailable, email not added to a group")
			case errors.Is(err, domain.ErrGroupFull):
				slog.WarnContext(ctx, "group is full, email not added to the group", "email_id", emailID, "group_id", redaction.RedactGroupHandle(groupID))
				return false
			default:
				slog.WarnContext(ctx, "failed to append email to group index", logging.ErrKey, err, "email_id", emailID, "group_id", redaction.RedactGroupHandle(groupID))
			}
			if newGroup {
				return false
			}
		}
	}
	record := api.EmailRecipientRecord{
		GroupID: groupID,
		EmailID: emailID,
		To:      req.To,
		Subject: req.Subject,
		SentAt:  time.Now().UTC(),
	}
	if err := h.store.WriteRecord(ctx, emailID, record); err != nil {
		slog.WarnContext(ctx, "failed to write recipient record to store", logging.ErrKey, err, "email_id", emailID)
	}
	return true
}

// oversizedFieldReason returns the error reply for the first header or envelope
// field of req that exceeds its length limit in pkg/api, or "" when all are
// within bounds.
func oversizedFieldReason(req api.SendEmailRequest) string {
	switch {
	case domain.CheckAddressLength(req.To) != nil:
		return "to address too long"
	case domain.CheckAddressLength(req.From) != nil:
		return "from address too long"
	case domain.CheckAddressLength(req.ReplyTo) != nil:
		return "reply_to address too long"
	case len(req.Subject) > api.MaxSubjectLength:
		return "subject too long"
	case len(req.FromDisplayName) > api.MaxFromDisplayNameLength:
		return "from_display_name too long"
	}
	return ""
}

// domainFromAddress extracts the host part of an RFC 5322 address for logging.
// It parses the address properly so display-name "@" characters do not
// interfere (e.g. `"Jane @ Home" <x@evil.com>` → `"evil.com"`).
// Falls back to returning the raw string if parsing fails.
func domainFromAddress(addr string) string {
	if parsed, err := mail.ParseAddress(addr); err == nil {
		if at := strings.LastIndex(parsed.Address, "@"); at >= 0 {
			return strings.ToLower(parsed.Address[at+1:])
		}
	}
	return addr
}

func replyError(ctx context.Context, respond func([]byte) error, reason string) {
	body, _ := json.Marshal(api.SendEmailErrorResponse{Error: reason})
	if err := respond(body); err != nil {
		slog.WarnContext(ctx, "failed to respond with error to NATS request", logging.ErrKey, err)
	}
}
