// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service/mocks"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// mockPublisher records Publish calls for assertions.
type mockPublisher struct {
	calls []publishCall
	err   error
}

type publishCall struct {
	subject string
	data    []byte
}

func (m *mockPublisher) Publish(subject string, data []byte) error {
	m.calls = append(m.calls, publishCall{subject: subject, data: data})
	return m.err
}

// sqsMessage builds an SQS message containing an SNS-wrapped SES event.
func sqsMessage(t *testing.T, eventType, emailID, groupID, timestamp string) types.Message {
	t.Helper()

	sesMsg := map[string]any{
		"eventType": eventType,
		"mail": map[string]any{
			"headers": []map[string]any{
				{"name": "X-LFX-TRACKING-ID", "value": groupID + "/" + emailID},
			},
		},
	}

	switch eventType {
	case "BOUNCE":
		sesMsg["bounce"] = map[string]any{"timestamp": timestamp}
	case "COMPLAINT":
		sesMsg["complaint"] = map[string]any{"timestamp": timestamp}
	case "DELIVERY":
		sesMsg["delivery"] = map[string]any{"timestamp": timestamp}
	case "Open":
		sesMsg["open"] = map[string]any{"timestamp": timestamp}
	}

	inner, err := json.Marshal(sesMsg)
	require.NoError(t, err)

	outer, err := json.Marshal(map[string]string{"MessageId": "sns-msg-1", "Message": string(inner)})
	require.NoError(t, err)

	body := string(outer)
	return types.Message{Body: &body}
}

func TestEngagementEventHandler_Handle_BouncePublish(t *testing.T) {
	t.Parallel()

	const (
		emailID   = "email-uuid-1"
		groupID   = "group-uuid-1"
		timestamp = "2026-01-02T15:04:05Z"
	)

	wantFailedAt, err := time.Parse(time.RFC3339, timestamp)
	require.NoError(t, err)
	wantFailedAt = wantFailedAt.UTC()

	tests := []struct {
		name        string
		eventType   string
		wantReason  string
		wantPublish bool
	}{
		{
			name:        "BOUNCE publishes with reason bounce",
			eventType:   "BOUNCE",
			wantReason:  "bounce",
			wantPublish: true,
		},
		{
			name:        "COMPLAINT publishes with reason complaint",
			eventType:   "COMPLAINT",
			wantReason:  "complaint",
			wantPublish: true,
		},
		{
			name:        "DELIVERY does not publish",
			eventType:   "DELIVERY",
			wantPublish: false,
		},
		{
			name:        "Open does not publish",
			eventType:   "Open",
			wantPublish: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := mocks.NewTrackingStore()
			store.PutRecord(emailID, api.EmailRecipientRecord{
				EmailID: emailID,
				GroupID: groupID,
			})

			pub := &mockPublisher{}
			handler := service.NewEngagementEventHandler(store).WithBouncePublisher(pub)

			msg := sqsMessage(t, tc.eventType, emailID, groupID, timestamp)
			err := handler.Handle(context.Background(), msg)
			require.NoError(t, err)

			if !tc.wantPublish {
				assert.Empty(t, pub.calls, "expected no publish calls for event type %s", tc.eventType)
				return
			}

			require.Len(t, pub.calls, 1)
			assert.Equal(t, api.EmailFailedSubject, pub.calls[0].subject)

			var evt api.EmailFailedEvent
			require.NoError(t, json.Unmarshal(pub.calls[0].data, &evt))
			assert.Equal(t, emailID, evt.EmailID)
			assert.Equal(t, groupID, evt.GroupID)
			assert.Equal(t, tc.wantReason, evt.Reason)
			assert.Equal(t, wantFailedAt, evt.FailedAt)
		})
	}
}

func TestEngagementEventHandler_Handle_NilPublisher(t *testing.T) {
	t.Parallel()

	// A nil publisher (no WithBouncePublisher call) must not panic on BOUNCE.
	store := mocks.NewTrackingStore()
	store.PutRecord("email-1", api.EmailRecipientRecord{EmailID: "email-1", GroupID: "group-1"})

	handler := service.NewEngagementEventHandler(store) // no publisher
	msg := sqsMessage(t, "BOUNCE", "email-1", "group-1", "2026-01-02T15:04:05Z")

	assert.NotPanics(t, func() {
		err := handler.Handle(context.Background(), msg)
		assert.NoError(t, err)
	})
}

func TestEngagementEventHandler_Handle_PublishFailure(t *testing.T) {
	t.Parallel()

	// A publish error must be logged but must not cause Handle to return an error.
	store := mocks.NewTrackingStore()
	store.PutRecord("email-1", api.EmailRecipientRecord{EmailID: "email-1", GroupID: "group-1"})

	pub := &mockPublisher{err: errors.New("nats: timeout")}
	handler := service.NewEngagementEventHandler(store).WithBouncePublisher(pub)

	msg := sqsMessage(t, "BOUNCE", "email-1", "group-1", "2026-01-02T15:04:05Z")
	err := handler.Handle(context.Background(), msg)

	assert.NoError(t, err, "publish failure must not propagate from Handle")
	assert.Len(t, pub.calls, 1, "publish was still attempted")

	// KV store record must still be updated despite the publish error.
	record, ok := store.GetStoredRecord("email-1")
	require.True(t, ok)
	assert.True(t, record.Failed)
}

func TestEngagementEventHandler_Handle_RecordNotFound_NoPublish(t *testing.T) {
	t.Parallel()

	// When the record doesn't exist in the store, Handle returns nil (SES late
	// event) and must not publish — there's nothing meaningful to notify about.
	store := mocks.NewTrackingStore() // empty — no record seeded
	pub := &mockPublisher{}
	handler := service.NewEngagementEventHandler(store).WithBouncePublisher(pub)

	msg := sqsMessage(t, "BOUNCE", "email-unknown", "group-1", "2026-01-02T15:04:05Z")
	err := handler.Handle(context.Background(), msg)

	assert.NoError(t, err)
	assert.Empty(t, pub.calls, "no publish when record not found")
}
