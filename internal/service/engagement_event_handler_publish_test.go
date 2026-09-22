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

// sqsMsg builds an SQS message containing an SNS-wrapped SES event.
func sqsMsg(t *testing.T, eventType, emailID, groupID, timestamp string, extra ...func(map[string]any)) types.Message {
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
	case "Click":
		sesMsg["click"] = map[string]any{
			"timestamp": timestamp,
			"link":      "https://example.com/link",
			"ipAddress": "1.2.3.4",
			"userAgent": "Mozilla/5.0",
		}
	}

	for _, fn := range extra {
		fn(sesMsg)
	}

	inner, err := json.Marshal(sesMsg)
	require.NoError(t, err)

	outer, err := json.Marshal(map[string]string{"MessageId": "sns-msg-1", "Message": string(inner)})
	require.NoError(t, err)

	body := string(outer)
	return types.Message{Body: &body}
}

const (
	testEmailID   = "email-uuid-1"
	testGroupID   = "group-uuid-1"
	testTimestamp = "2026-01-02T15:04:05Z"
)

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return ts.UTC()
}

func seedRecord(store *mocks.TrackingStore) {
	store.PutRecord(testEmailID, api.EmailRecipientRecord{
		EmailID: testEmailID,
		GroupID: testGroupID,
	})
}

// ---------------------------------------------------------------------------
// Happy-path publish tests — one per SES event type
// ---------------------------------------------------------------------------

func TestEngagementEventHandler_Handle_Publish_Bounce(t *testing.T) {
	t.Parallel()

	store := mocks.NewTrackingStore()
	seedRecord(store)
	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	msg := sqsMsg(t, "BOUNCE", testEmailID, testGroupID, testTimestamp)
	require.NoError(t, h.Handle(context.Background(), msg))

	require.Len(t, pub.calls, 1)
	assert.Equal(t, api.EmailFailedSubject, pub.calls[0].subject)

	var evt api.EmailFailedEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &evt))
	assert.Equal(t, testEmailID, evt.EmailID)
	assert.Equal(t, testGroupID, evt.GroupID)
	assert.Equal(t, "bounce", evt.Reason)
	assert.Equal(t, mustParseTime(t, testTimestamp), evt.FailedAt)
}

func TestEngagementEventHandler_Handle_Publish_Complaint(t *testing.T) {
	t.Parallel()

	store := mocks.NewTrackingStore()
	seedRecord(store)
	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	msg := sqsMsg(t, "COMPLAINT", testEmailID, testGroupID, testTimestamp)
	require.NoError(t, h.Handle(context.Background(), msg))

	require.Len(t, pub.calls, 1)
	assert.Equal(t, api.EmailFailedSubject, pub.calls[0].subject)

	var evt api.EmailFailedEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &evt))
	assert.Equal(t, "complaint", evt.Reason)
}

func TestEngagementEventHandler_Handle_Publish_Delivery(t *testing.T) {
	t.Parallel()

	store := mocks.NewTrackingStore()
	seedRecord(store)
	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	msg := sqsMsg(t, "DELIVERY", testEmailID, testGroupID, testTimestamp)
	require.NoError(t, h.Handle(context.Background(), msg))

	require.Len(t, pub.calls, 1)
	assert.Equal(t, api.EmailDeliveredSubject, pub.calls[0].subject)

	var evt api.EmailDeliveredEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &evt))
	assert.Equal(t, testEmailID, evt.EmailID)
	assert.Equal(t, testGroupID, evt.GroupID)
	assert.Equal(t, mustParseTime(t, testTimestamp), evt.DeliveredAt)
}

func TestEngagementEventHandler_Handle_Publish_Open(t *testing.T) {
	t.Parallel()

	store := mocks.NewTrackingStore()
	seedRecord(store)
	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	msg := sqsMsg(t, "Open", testEmailID, testGroupID, testTimestamp)
	require.NoError(t, h.Handle(context.Background(), msg))

	require.Len(t, pub.calls, 1)
	assert.Equal(t, api.EmailOpenedSubject, pub.calls[0].subject)

	var evt api.EmailOpenedEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &evt))
	assert.Equal(t, testEmailID, evt.EmailID)
	assert.Equal(t, testGroupID, evt.GroupID)
	assert.Equal(t, 1, evt.OpenCount)
	assert.Equal(t, mustParseTime(t, testTimestamp), evt.OpenedAt)
}

func TestEngagementEventHandler_Handle_Publish_Click(t *testing.T) {
	t.Parallel()

	store := mocks.NewTrackingStore()
	seedRecord(store)
	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	msg := sqsMsg(t, "Click", testEmailID, testGroupID, testTimestamp)
	require.NoError(t, h.Handle(context.Background(), msg))

	require.Len(t, pub.calls, 1)
	assert.Equal(t, api.EmailLinkClickedSubject, pub.calls[0].subject)

	var evt api.EmailLinkClickedEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &evt))
	assert.Equal(t, testEmailID, evt.EmailID)
	assert.Equal(t, testGroupID, evt.GroupID)
	assert.Equal(t, "https://example.com/link", evt.Link)
	assert.Equal(t, 1, evt.ClickCount)
	assert.Equal(t, mustParseTime(t, testTimestamp), evt.ClickedAt)
}

// Multiple clicks on different links each produce a separate publish.
func TestEngagementEventHandler_Handle_Publish_Click_MultipleLinks(t *testing.T) {
	t.Parallel()

	store := mocks.NewTrackingStore()
	seedRecord(store)
	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	// Build two click messages with distinct SNS MessageIds and links.
	makeClickMsg := func(snsID, link string) types.Message {
		sesMsg := map[string]any{
			"eventType": "Click",
			"mail": map[string]any{
				"headers": []map[string]any{
					{"name": "X-LFX-TRACKING-ID", "value": testGroupID + "/" + testEmailID},
				},
			},
			"click": map[string]any{
				"timestamp": testTimestamp,
				"link":      link,
			},
		}
		inner, _ := json.Marshal(sesMsg)
		outer, _ := json.Marshal(map[string]string{"MessageId": snsID, "Message": string(inner)})
		body := string(outer)
		return types.Message{Body: &body}
	}

	require.NoError(t, h.Handle(context.Background(), makeClickMsg("sns-1", "https://example.com/a")))
	require.NoError(t, h.Handle(context.Background(), makeClickMsg("sns-2", "https://example.com/b")))

	require.Len(t, pub.calls, 2)

	var evt1, evt2 api.EmailLinkClickedEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &evt1))
	require.NoError(t, json.Unmarshal(pub.calls[1].data, &evt2))

	assert.Equal(t, "https://example.com/a", evt1.Link)
	assert.Equal(t, 1, evt1.ClickCount)
	assert.Equal(t, "https://example.com/b", evt2.Link)
	assert.Equal(t, 2, evt2.ClickCount)
}

// KV record also stores click data.
func TestEngagementEventHandler_Handle_Click_StoresInKV(t *testing.T) {
	t.Parallel()

	store := mocks.NewTrackingStore()
	seedRecord(store)
	h := service.NewEngagementEventHandler(store)

	msg := sqsMsg(t, "Click", testEmailID, testGroupID, testTimestamp)
	require.NoError(t, h.Handle(context.Background(), msg))

	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)
	assert.True(t, record.Clicked)
	assert.Equal(t, 1, record.ClickCount)
	require.Len(t, record.ClickList, 1)
	assert.Equal(t, "https://example.com/link", record.ClickList[0].Link)
	assert.NotNil(t, record.LastClickedAt)
}

// ---------------------------------------------------------------------------
// Edge-case / error tests
// ---------------------------------------------------------------------------

func TestEngagementEventHandler_Handle_NilPublisher_NoPanic(t *testing.T) {
	t.Parallel()

	// A handler with no publisher must not panic on any event type.
	store := mocks.NewTrackingStore()
	seedRecord(store)
	h := service.NewEngagementEventHandler(store)

	for _, eventType := range []string{"BOUNCE", "COMPLAINT", "DELIVERY", "Open", "Click"} {
		t.Run(eventType, func(t *testing.T) {
			t.Parallel()
			assert.NotPanics(t, func() {
				_ = h.Handle(context.Background(), sqsMsg(t, eventType, testEmailID, testGroupID, testTimestamp))
			})
		})
	}
}

func TestEngagementEventHandler_Handle_PublishFailure_DoesNotPropagateFromHandle(t *testing.T) {
	t.Parallel()

	store := mocks.NewTrackingStore()
	seedRecord(store)
	pub := &mockPublisher{err: errors.New("nats: connection timeout")}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	for _, eventType := range []string{"BOUNCE", "DELIVERY", "Open", "Click"} {
		// Re-seed the record before each sub-test so state is clean.
		seedRecord(store)
		msg := sqsMsg(t, eventType, testEmailID, testGroupID, testTimestamp)
		err := h.Handle(context.Background(), msg)
		assert.NoError(t, err, "publish failure for %s must not propagate from Handle", eventType)
		assert.NotEmpty(t, pub.calls, "publish was still attempted for %s", eventType)
		pub.calls = nil // reset for next iteration
	}
}

func TestEngagementEventHandler_Handle_RecordNotFound_NoPublish(t *testing.T) {
	t.Parallel()

	// When the record doesn't exist (late-arriving SES event), Handle must
	// return nil and must not publish anything.
	store := mocks.NewTrackingStore() // empty
	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	for _, eventType := range []string{"BOUNCE", "DELIVERY", "Open", "Click"} {
		msg := sqsMsg(t, eventType, "unknown-email", testGroupID, testTimestamp)
		require.NoError(t, h.Handle(context.Background(), msg))
	}

	assert.Empty(t, pub.calls, "no publish when record not found")
}
