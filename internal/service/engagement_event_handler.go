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

// maxClickListEntries caps the number of ClickEvent entries stored in a KV
// record. The email-recipients bucket has maxValueSize 65 536 bytes; each
// ClickEvent is roughly 300–2 000 bytes depending on URL length, so an
// unbounded list would overflow the bucket and make every subsequent CAS
// update fail. Unique click deduplication is still performed against all
// stored entries; events beyond the cap still update ClickCount and
// LastClickedAt but are not appended to ClickList.
const maxClickListEntries = 100

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
	Click     *sesClick     `json:"click"`
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

// sesClick corresponds to the SES "Click" event, fired when a recipient clicks
// a tracked link in the email.
type sesClick struct {
	Timestamp string `json:"timestamp"`
	Link      string `json:"link"`
	IPAddress string `json:"ipAddress"`
	UserAgent string `json:"userAgent"`
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

// EngagementPublisher publishes engagement event notifications to NATS subjects.
// *natsgo.Conn satisfies this interface directly.
type EngagementPublisher interface {
	Publish(subject string, data []byte) error
}

// EngagementEventHandler parses SES engagement events from SQS and updates the recipients store.
type EngagementEventHandler struct {
	store     domain.TrackingStore
	publisher EngagementPublisher // nil → publish step is skipped
}

// NewEngagementEventHandler creates a handler that updates records via store.
func NewEngagementEventHandler(store domain.TrackingStore) *EngagementEventHandler {
	return &EngagementEventHandler{store: store}
}

// WithEngagementPublisher returns a copy of h configured to publish engagement
// events (delivered, opened, link clicked, failed) to publisher as each SES
// event is processed. Publishing is best-effort: a failure is logged but does
// not affect the KV store update or the return value of Handle.
func (h *EngagementEventHandler) WithEngagementPublisher(p EngagementPublisher) *EngagementEventHandler {
	return &EngagementEventHandler{store: h.store, publisher: p}
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
	case "OPEN", "CLICK", "DELIVERY", "BOUNCE", "COMPLAINT":
	default:
		slog.DebugContext(ctx, "ignoring unknown ses event type", "event_type", event.EventType)
		return nil
	}

	slog.DebugContext(ctx, "ses engagement event received", "event_type", strings.ToLower(eventType))

	// Capture values from the record inside the update callback so they are
	// available for the publish step below, without a second store round-trip.
	// eventApplied is set only when applyEngagementEvent actually wrote new data
	// (i.e. the event was not a deduplicated replay); publishing is skipped for
	// replays to avoid duplicate downstream notifications.
	var (
		capturedGroupID    string
		capturedAt         time.Time
		capturedOpenCount  int
		capturedClickLink  string
		capturedClickCount int
		eventApplied       bool
	)

	err := h.store.UpdateRecord(ctx, emailID, func(record *api.EmailRecipientRecord) {
		eventApplied = applyEngagementEvent(record, eventType, env.MessageID, event)
		if !eventApplied {
			return // deduplicated or already-set; skip capture
		}
		capturedGroupID = record.GroupID
		// Read timestamps directly from the current SES event rather than from
		// record aggregate fields (e.g. LastOpenedAt). Aggregate fields hold the
		// maximum value across all events, which is incorrect when an older SES
		// event arrives out-of-order after a newer one.
		switch eventType {
		case "DELIVERY":
			var ts string
			if event.Delivery != nil {
				ts = event.Delivery.Timestamp
			}
			capturedAt = parseTimestamp(ts)
		case "OPEN":
			var ts string
			if event.Open != nil {
				ts = event.Open.Timestamp
			}
			capturedAt = parseTimestamp(ts)
			capturedOpenCount = record.OpenCount
		case "CLICK":
			var ts string
			if event.Click != nil {
				ts = event.Click.Timestamp
				capturedClickLink = event.Click.Link
			}
			capturedAt = parseTimestamp(ts)
			capturedClickCount = record.ClickCount
		case "BOUNCE":
			var ts string
			if event.Bounce != nil {
				ts = event.Bounce.Timestamp
			}
			capturedAt = parseTimestamp(ts)
		case "COMPLAINT":
			var ts string
			if event.Complaint != nil {
				ts = event.Complaint.Timestamp
			}
			capturedAt = parseTimestamp(ts)
		}
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update recipient record", logging.ErrKey, err)
		return fmt.Errorf("store update for email_id %s: %w", emailID, err)
	}

	slog.DebugContext(ctx, "ses engagement event applied", "event_type", strings.ToLower(eventType))

	if eventApplied && h.publisher != nil {
		h.publishEngagementEvent(ctx, emailID, eventType, capturedGroupID, capturedAt, capturedOpenCount, capturedClickCount, capturedClickLink)
	}
	return nil
}

// publishEngagementEvent marshals and publishes the appropriate engagement
// event payload for the given SES event type. Failures are logged but do not
// propagate — publishing is best-effort.
func (h *EngagementEventHandler) publishEngagementEvent(
	ctx context.Context,
	emailID, eventType, groupID string,
	at time.Time,
	openCount, clickCount int,
	clickLink string,
) {
	var subject string
	var payload any

	switch eventType {
	case "DELIVERY":
		subject = api.EmailDeliveredSubject
		payload = api.EmailDeliveredEvent{EmailID: emailID, GroupID: groupID, DeliveredAt: at}
	case "OPEN":
		subject = api.EmailOpenedSubject
		payload = api.EmailOpenedEvent{EmailID: emailID, GroupID: groupID, OpenCount: openCount, OpenedAt: at}
	case "CLICK":
		subject = api.EmailLinkClickedSubject
		payload = api.EmailLinkClickedEvent{EmailID: emailID, GroupID: groupID, Link: clickLink, ClickCount: clickCount, ClickedAt: at}
	case "BOUNCE":
		subject = api.EmailFailedSubject
		payload = api.EmailFailedEvent{EmailID: emailID, GroupID: groupID, Reason: "bounce", FailedAt: at}
	case "COMPLAINT":
		subject = api.EmailFailedSubject
		payload = api.EmailFailedEvent{EmailID: emailID, GroupID: groupID, Reason: "complaint", FailedAt: at}
	default:
		return
	}

	data, err := json.Marshal(payload)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal engagement event", logging.ErrKey, err,
			"email_id", emailID, "event_type", strings.ToLower(eventType))
		return
	}
	if err := h.publisher.Publish(subject, data); err != nil {
		slog.WarnContext(ctx, "failed to publish engagement event", logging.ErrKey, err,
			"email_id", emailID, "event_type", strings.ToLower(eventType))
	}
}

// applyEngagementEvent updates record fields based on the SES event type,
// using SES-provided timestamps when available and falling back to time.Now().
// snsMessageID deduplicates replayed OPEN and CLICK events.
// Returns true when the record was modified, false when the event was a
// no-op (duplicate SNS MessageId for OPEN/CLICK, or state already set for
// single-fire events DELIVERY/BOUNCE/COMPLAINT).
func applyEngagementEvent(record *api.EmailRecipientRecord, eventType, snsMessageID string, event sesEvent) bool {
	switch eventType {
	case "OPEN":
		for _, e := range record.OpenedAtList {
			if e.EventID == snsMessageID {
				return false // already processed this SNS delivery
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
		return true
	case "CLICK":
		for _, c := range record.ClickList {
			if c.EventID == snsMessageID {
				return false // already processed this SNS delivery
			}
		}
		var ts, link string
		if event.Click != nil {
			ts = event.Click.Timestamp
			link = event.Click.Link
		}
		t := parseTimestamp(ts)
		record.Clicked = true
		record.ClickCount++
		if len(record.ClickList) < maxClickListEntries {
			record.ClickList = append(record.ClickList, api.ClickEvent{EventID: snsMessageID, Link: link, ClickedAt: t})
		}
		if record.LastClickedAt == nil || t.After(*record.LastClickedAt) {
			record.LastClickedAt = &t
		}
		return true
	case "DELIVERY":
		if record.Delivered {
			return false
		}
		var ts string
		if event.Delivery != nil {
			ts = event.Delivery.Timestamp
		}
		t := parseTimestamp(ts)
		record.Delivered = true
		record.DeliveredAt = &t
		return true
	case "BOUNCE":
		if record.Failed {
			return false
		}
		var ts string
		if event.Bounce != nil {
			ts = event.Bounce.Timestamp
		}
		t := parseTimestamp(ts)
		record.Failed = true
		record.FailedAt = &t
		return true
	case "COMPLAINT":
		if record.Failed {
			return false
		}
		var ts string
		if event.Complaint != nil {
			ts = event.Complaint.Timestamp
		}
		t := parseTimestamp(ts)
		record.Failed = true
		record.FailedAt = &t
		return true
	}
	return false
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
