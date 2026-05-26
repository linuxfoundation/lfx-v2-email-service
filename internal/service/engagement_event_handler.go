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
	EventType string        `json:"eventType"`
	Mail      sesMail       `json:"mail"`
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

type sesBounce struct {
	Timestamp string `json:"timestamp"`
}

type sesComplaint struct {
	Timestamp string `json:"timestamp"`
}

type sesDelivery struct {
	Timestamp string `json:"timestamp"`
}

// EngagementEventHandler parses SES engagement events from SQS and updates the recipients KV bucket.
type EngagementEventHandler struct {
	recipientsKV natsgo.KeyValue
}

// NewEngagementEventHandler creates a handler that writes to recipientsKV.
func NewEngagementEventHandler(recipientsKV natsgo.KeyValue) *EngagementEventHandler {
	return &EngagementEventHandler{recipientsKV: recipientsKV}
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

	entry, err := h.recipientsKV.Get(emailID)
	if err != nil {
		slog.DebugContext(ctx, "no recipient record for email_id, skipping", "email_id", emailID)
		return nil
	}

	var record api.EmailRecipientRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		slog.WarnContext(ctx, "failed to unmarshal recipient record", logging.ErrKey, err, "email_id", emailID)
		return nil
	}

	now := time.Now().UTC()
	eventType := strings.ToUpper(event.EventType)
	switch eventType {
	case "OPEN":
		if !record.Opened {
			record.Opened = true
			record.OpenedAt = &now
		}
	case "DELIVERY":
		if !record.Delivered {
			record.Delivered = true
			record.DeliveredAt = &now
		}
	case "BOUNCE", "COMPLAINT":
		if !record.Failed {
			record.Failed = true
			record.FailedAt = &now
		}
	default:
		slog.DebugContext(ctx, "ignoring unknown ses event type", "event_type", event.EventType)
		return nil
	}

	updated, err := json.Marshal(record)
	if err != nil {
		slog.WarnContext(ctx, "failed to marshal updated recipient record", logging.ErrKey, err)
		return nil
	}
	if _, err := h.recipientsKV.Put(emailID, updated); err != nil {
		slog.WarnContext(ctx, "failed to update recipient record in KV", logging.ErrKey, err, "email_id", emailID)
	}

	return nil
}

// extractEmailID finds the X-LFX-TRACKING-ID header (format: group_id/email_id)
// and returns the email_id portion (everything after the first '/').
func extractEmailID(headers []sesHeader) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, "X-LFX-TRACKING-ID") {
			v := strings.TrimSpace(h.Value)
			if idx := strings.Index(v, "/"); idx != -1 {
				return v[idx+1:]
			}
			return v
		}
	}
	return ""
}
