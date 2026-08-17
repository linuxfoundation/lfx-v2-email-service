// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// snsEnvelope is the outer SNS notification wrapper around the SES event JSON.
type snsEnvelope struct {
	MessageID string `json:"MessageId"`
	Message   string `json:"Message"`
}

// sesEvent is the parsed SES engagement event.
type sesEvent struct {
	EventType string        `json:"eventType"`
	Mail      sesMail       `json:"mail"`
	Open      *sesOpen      `json:"open"`
	Bounce    *sesBounce    `json:"bounce"`
	Complaint *sesComplaint `json:"complaint"`
	Delivery  *sesDelivery  `json:"delivery"`
}

type sesMail struct {
	Headers []sesHeader `json:"headers"`
}

type sesHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type sesOpen struct {
	Timestamp string `json:"timestamp"`
}

type sesBounce struct {
	Timestamp string `json:"timestamp"`
}

type sesComplaint struct {
	Timestamp string `json:"timestamp"`
}

type sesDelivery struct {
	Timestamp string `json:"timestamp"`
}

// EngagementEventHandler parses SES engagement events from SQS and updates the recipients store.
type EngagementEventHandler struct {
	store domain.TrackingStore
}

// NewEngagementEventHandler creates a handler that updates records via store.
func NewEngagementEventHandler(store domain.TrackingStore) *EngagementEventHandler {
	return &EngagementEventHandler{store: store}
}

// Handle processes a single SQS message containing an SNS-wrapped SES event.
func (h *EngagementEventHandler) Handle(ctx context.Context, msg types.Message) error {
	body := ""
	if msg.Body != nil {
		body = *msg.Body
	}

	var env snsEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal sns envelope", logging.ErrKey, err)
		return nil // non-retryable: delete the message
	}

	var event sesEvent
	if err := json.Unmarshal([]byte(env.Message), &event); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal ses event", logging.ErrKey, err)
		return nil
	}

	emailID := extractEmailID(event.Mail.Headers)
	if emailID == "" {
		slog.WarnContext(ctx, "ses event missing X-LFX-TRACKING-ID header, skipping")
		return nil
	}

	ctx = logging.AppendCtx(ctx, slog.String("email_id", emailID))

	eventType := strings.ToUpper(event.EventType)
	switch eventType {
	case "OPEN", "DELIVERY", "BOUNCE", "COMPLAINT":
	default:
		slog.DebugContext(ctx, "ignoring unknown ses event type", "event_type", event.EventType)
		return nil
	}

	slog.DebugContext(ctx, "ses engagement event received", "event_type", strings.ToLower(eventType))

	err := h.store.UpdateRecord(ctx, emailID, func(record *api.EmailRecipientRecord) {
		applyEngagementEvent(record, eventType, env.MessageID, event)
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update recipient record", logging.ErrKey, err)
		return fmt.Errorf("store update for email_id %s: %w", emailID, err)
	}

	slog.DebugContext(ctx, "ses engagement event applied", "event_type", strings.ToLower(eventType))
	return nil
}

// applyEngagementEvent updates record fields based on the SES event type,
// using SES-provided timestamps when available and falling back to time.Now().
// snsMessageID is used to deduplicate replayed open events.
func applyEngagementEvent(record *api.EmailRecipientRecord, eventType, snsMessageID string, event sesEvent) {
	switch eventType {
	case "OPEN":
		for _, e := range record.OpenedAtList {
			if e.EventID == snsMessageID {
				return // already processed this SNS delivery
			}
		}
		var ts string
		if event.Open != nil {
			ts = event.Open.Timestamp
		}
		t := parseTimestamp(ts)
		record.Opened = true
		record.OpenedAtList = append(record.OpenedAtList, api.OpenEvent{EventID: snsMessageID, OpenedAt: t})
		record.OpenCount = len(record.OpenedAtList)
		if record.LastOpenedAt == nil || t.After(*record.LastOpenedAt) {
			record.LastOpenedAt = &t
		}
	case "DELIVERY":
		if !record.Delivered {
			var ts string
			if event.Delivery != nil {
				ts = event.Delivery.Timestamp
			}
			t := parseTimestamp(ts)
			record.Delivered = true
			record.DeliveredAt = &t
		}
	case "BOUNCE":
		if !record.Failed {
			var ts string
			if event.Bounce != nil {
				ts = event.Bounce.Timestamp
			}
			t := parseTimestamp(ts)
			record.Failed = true
			record.FailedAt = &t
		}
	case "COMPLAINT":
		if !record.Failed {
			var ts string
			if event.Complaint != nil {
				ts = event.Complaint.Timestamp
			}
			t := parseTimestamp(ts)
			record.Failed = true
			record.FailedAt = &t
		}
	}
}

// parseTimestamp parses an RFC3339 timestamp string, falling back to time.Now().UTC().
func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

// extractEmailID finds the X-LFX-TRACKING-ID header (format: group_id/email_id)
// and returns the email_id portion (everything after the last '/').
// Splitting on the last '/' means a group_id that itself contains '/' is handled safely.
func extractEmailID(headers []sesHeader) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, "X-LFX-TRACKING-ID") {
			v := strings.TrimSpace(h.Value)
			if idx := strings.LastIndex(v, "/"); idx != -1 {
				return v[idx+1:]
			}
			return v
		}
	}
	return ""
}
