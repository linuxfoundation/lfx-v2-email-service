// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// snsEnvelope is the outer SNS notification wrapper around the SES event JSON.
type snsEnvelope struct {
	Message string `json:"Message"`
}

// sesEvent is the parsed SES engagement event.
type sesEvent struct {
	EventType string   `json:"eventType"`
	Mail      sesMail  `json:"mail"`
	Open      *sesOpen `json:"open"`
	Bounce    *sesBounce `json:"bounce"`
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

// EngagementEventHandler parses SES engagement events from SQS and updates the tracking KV bucket.
type EngagementEventHandler struct {
	kvStore natsgo.KeyValue
	nc      *natsgo.Conn
}

// NewEngagementEventHandler creates a handler that writes to kvStore and publishes on nc.
func NewEngagementEventHandler(kvStore natsgo.KeyValue, nc *natsgo.Conn) *EngagementEventHandler {
	return &EngagementEventHandler{kvStore: kvStore, nc: nc}
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

	messageID := extractMessageID(event.Mail.Headers)
	if messageID == "" {
		slog.WarnContext(ctx, "ses event missing Message-ID header, skipping")
		return nil
	}

	kvKey := "ses-message-id/" + messageID

	entry, err := h.kvStore.Get(kvKey)
	if err != nil {
		slog.DebugContext(ctx, "no tracking record for message, skipping", "key", kvKey)
		return nil
	}

	var record api.EmailTrackingRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal tracking record", logging.ErrKey, err, "key", kvKey)
		return nil
	}

	eventType := strings.ToUpper(event.EventType)
	switch eventType {
	case "OPEN":
		h.applyOpen(ctx, &record, &event)
	case "BOUNCE":
		h.applyBounce(&record, &event)
	case "COMPLAINT":
		h.applyComplaint(&record, &event)
	case "DELIVERY":
		h.applyDelivery(&record, &event)
	default:
		slog.DebugContext(ctx, "ignoring unknown ses event type", "event_type", event.EventType)
		return nil
	}

	updated, err := json.Marshal(record)
	if err != nil {
		slog.WarnContext(ctx, "failed to marshal updated tracking record", logging.ErrKey, err)
		return nil
	}
	if _, err := h.kvStore.Put(kvKey, updated); err != nil {
		slog.WarnContext(ctx, "failed to update tracking record in KV", logging.ErrKey, err, "key", kvKey)
		return nil
	}

	if eventType == "OPEN" {
		if err := h.nc.Publish(api.EmailOpenedSubject, updated); err != nil {
			slog.WarnContext(ctx, "failed to publish email-opened event", logging.ErrKey, err)
		}
	}

	return nil
}

func (h *EngagementEventHandler) applyOpen(ctx context.Context, record *api.EmailTrackingRecord, event *sesEvent) {
	now := time.Now().UTC()
	record.OpenCount++
	if record.FirstOpenedAt == nil {
		record.FirstOpenedAt = &now
		slog.DebugContext(ctx, "first open recorded", "message_id", record.SESMessageID)
	}
	record.LastOpenedAt = &now
}

func (h *EngagementEventHandler) applyBounce(record *api.EmailTrackingRecord, event *sesEvent) {
	if record.BouncedAt != nil {
		return
	}
	now := time.Now().UTC()
	record.BouncedAt = &now
}

func (h *EngagementEventHandler) applyComplaint(record *api.EmailTrackingRecord, event *sesEvent) {
	if record.ComplainedAt != nil {
		return
	}
	now := time.Now().UTC()
	record.ComplainedAt = &now
}

func (h *EngagementEventHandler) applyDelivery(record *api.EmailTrackingRecord, event *sesEvent) {
	if record.DeliveredAt != nil {
		return
	}
	now := time.Now().UTC()
	record.DeliveredAt = &now
}

// extractMessageID finds the Message-ID header value from the mail headers and strips angle brackets.
func extractMessageID(headers []sesHeader) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Message-ID") {
			v := strings.TrimSpace(h.Value)
			v = strings.TrimPrefix(v, "<")
			v = strings.TrimSuffix(v, ">")
			return v
		}
	}
	return ""
}
