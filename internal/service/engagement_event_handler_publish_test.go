// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

// Replaying an event with the same SNS MessageId must result in exactly one KV
// entry and exactly one NATS publish. SQS delivers at-least-once; the second
// delivery must be silently dropped without emitting a duplicate notification.
func TestEngagementEventHandler_Handle_Deduplication_SameMessageID(t *testing.T) {
	t.Parallel()

	for _, eventType := range []string{"Open", "Click"} {
		eventType := eventType
		t.Run(eventType, func(t *testing.T) {
			t.Parallel()

			store := mocks.NewTrackingStore()
			seedRecord(store)
			pub := &mockPublisher{}
			h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

			// sqsMsg always uses SNS MessageId "sns-msg-1"; calling Handle twice
			// with the same message simulates an SQS at-least-once re-delivery.
			msg := sqsMsg(t, eventType, testEmailID, testGroupID, testTimestamp)
			require.NoError(t, h.Handle(context.Background(), msg))
			require.NoError(t, h.Handle(context.Background(), msg))

			// The replay is a no-op: only one NATS event must have been published.
			assert.Len(t, pub.calls, 1, "replay must not emit a second NATS notification")

			// The KV record must contain exactly one entry, not two.
			record, ok := store.GetStoredRecord(testEmailID)
			require.True(t, ok)
			switch eventType {
			case "Open":
				assert.Equal(t, 1, record.OpenCount, "deduplication must not double-count opens")
			case "Click":
				assert.Equal(t, 1, record.ClickCount, "deduplication must not double-count clicks")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// KV byte-budget boundary tests
//
// These tests exercise the three distinct outcomes of the byte-budget guard in
// applyEngagementEvent when the record is near the 50 KB soft ceiling:
//
//   1. Only the ClickList history entry is rolled back (dedup ID fits).
//   2. Both the ClickList entry and the ClickEventID are rolled back.
//   3. The bounded-dedup-window: once no ID can be stored, replays are not
//      detected and re-increment ClickCount (documented trade-off).
//
// The subject field is used to pad the record to a predictable size. No
// omitempty tag is applied to it so it is always serialised.
// ---------------------------------------------------------------------------

// makeClickSNSMsg builds an SQS message wrapping a Click SES event with a
// caller-supplied SNS MessageId and link, bypassing the sqsMsg helper's fixed
// MessageId "sns-msg-1".
func makeClickSNSMsg(t *testing.T, snsID, link, emailID, groupID, timestamp string) types.Message {
	t.Helper()
	sesMsg := map[string]any{
		"eventType": "Click",
		"mail": map[string]any{
			"headers": []map[string]any{
				{"name": "X-LFX-TRACKING-ID", "value": groupID + "/" + emailID},
			},
		},
		"click": map[string]any{
			"timestamp": timestamp,
			"link":      link,
			"ipAddress": "1.2.3.4",
			"userAgent": "Mozilla/5.0",
		},
	}
	inner, err := json.Marshal(sesMsg)
	if err != nil {
		t.Fatalf("makeClickSNSMsg: marshal inner: %v", err)
	}
	outer, err := json.Marshal(map[string]string{"MessageId": snsID, "Message": string(inner)})
	if err != nil {
		t.Fatalf("makeClickSNSMsg: marshal outer: %v", err)
	}
	body := string(outer)
	return types.Message{Body: &body}
}

// TestEngagementEventHandler_Click_HistoryRolledBackAtSizeLimit verifies that
// when a new ClickList entry would push the record over the 50 KB soft ceiling
// but the 36-byte dedup ID alone fits, only the history entry is rolled back.
// ClickCount and the dedup ID are still recorded.
func TestEngagementEventHandler_Click_HistoryRolledBackAtSizeLimit(t *testing.T) {
	t.Parallel()

	// Subject padded to ~48 000 chars gives a base record of ~48 200 bytes after
	// Clicked/ClickCount modifications — well under the 50 KB ceiling.
	// A 2 000-char link entry adds ~2 000 bytes and would push the total past 50 KB,
	// while the ~31-byte dedup ID alone keeps the record comfortably below the limit.
	const subjectLen = 48_000
	const longLinkLen = 2_000

	store := mocks.NewTrackingStore()
	store.PutRecord(testEmailID, api.EmailRecipientRecord{
		EmailID: testEmailID,
		GroupID: testGroupID,
		Subject: strings.Repeat("x", subjectLen),
	})

	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	longLink := strings.Repeat("l", longLinkLen)
	msg := makeClickSNSMsg(t, "sns-big-link", longLink, testEmailID, testGroupID, testTimestamp)
	require.NoError(t, h.Handle(context.Background(), msg))

	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)

	assert.Equal(t, 1, record.ClickCount, "ClickCount must be incremented")
	assert.Len(t, record.ClickEventIDs, 1, "dedup ID must be retained when it fits within the size budget")
	assert.Empty(t, record.ClickList, "ClickList entry must be rolled back to keep the record within the KV limit")

	// The handler still publishes: the click was counted.
	require.Len(t, pub.calls, 1)
	assert.Equal(t, api.EmailLinkClickedSubject, pub.calls[0].subject)

	// Final serialised record must fit within the KV bucket hard limit (65 536 bytes).
	b, err := json.Marshal(record)
	require.NoError(t, err)
	assert.Less(t, len(b), 65_536, "serialised record must remain within the KV bucket hard limit")
}

// TestEngagementEventHandler_Click_IDRolledBackWhenRecordFull verifies that when
// the base record is already at or above the 50 KB soft ceiling, both the ClickList
// entry and the dedup ID are rolled back. ClickCount is still incremented because
// that is a scalar increment that occurs unconditionally before the size check.
func TestEngagementEventHandler_Click_IDRolledBackWhenRecordFull(t *testing.T) {
	t.Parallel()

	// A 49 800-char subject produces a base record of ~50 000 bytes — at or just
	// above the soft ceiling — so even the 31-byte dedup ID would overflow it.
	const subjectLen = 49_800

	store := mocks.NewTrackingStore()
	store.PutRecord(testEmailID, api.EmailRecipientRecord{
		EmailID: testEmailID,
		GroupID: testGroupID,
		Subject: strings.Repeat("x", subjectLen),
	})

	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	msg := makeClickSNSMsg(t, "sns-overflow", "https://example.com/link", testEmailID, testGroupID, testTimestamp)
	require.NoError(t, h.Handle(context.Background(), msg))

	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)

	assert.Equal(t, 1, record.ClickCount, "ClickCount must be incremented")
	assert.Empty(t, record.ClickEventIDs, "dedup ID must be rolled back when even the ID alone overflows the budget")
	assert.Empty(t, record.ClickList, "ClickList must be empty")

	// The handler still publishes the counted click.
	require.Len(t, pub.calls, 1)
	assert.Equal(t, api.EmailLinkClickedSubject, pub.calls[0].subject)
}

// TestEngagementEventHandler_Click_BoundedDedupWindow verifies the documented
// bounded-dedup-window behaviour: once the KV record is full and no dedup ID can
// be stored, a replay of the same SNS MessageId passes the dedup check and
// re-increments ClickCount and emits an additional NATS push. This test
// intentionally asserts the replay IS counted to confirm the documented trade-off.
func TestEngagementEventHandler_Click_BoundedDedupWindow(t *testing.T) {
	t.Parallel()

	const subjectLen = 49_800

	store := mocks.NewTrackingStore()
	store.PutRecord(testEmailID, api.EmailRecipientRecord{
		EmailID: testEmailID,
		GroupID: testGroupID,
		Subject: strings.Repeat("x", subjectLen),
	})

	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	msg := makeClickSNSMsg(t, "sns-overflow", "https://example.com/link", testEmailID, testGroupID, testTimestamp)

	// First delivery.
	require.NoError(t, h.Handle(context.Background(), msg))
	// SQS replay of the same message — dedup ID was not stored, so it is not detected.
	require.NoError(t, h.Handle(context.Background(), msg))

	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)

	// Both the original and the replay increment ClickCount: this is the
	// bounded-dedup-window trade-off documented in the contract.
	assert.Equal(t, 2, record.ClickCount,
		"replay must increment ClickCount once the dedup state is exhausted (bounded window)")
	assert.Len(t, pub.calls, 2,
		"each counted click emits a NATS notification, including the undetected replay")
}

// ---------------------------------------------------------------------------
// redactLink integration — stored KV value and published NATS event must
// never retain query or fragment components from SES tracked URLs.
// ---------------------------------------------------------------------------

// TestEngagementEventHandler_Click_LinkRedaction_StoredAndPublished confirms
// that when a Click event arrives with a URL that contains a query string and
// a fragment, both the record persisted to the KV store and the NATS publish
// payload have those components stripped.
func TestEngagementEventHandler_Click_LinkRedaction_StoredAndPublished(t *testing.T) {
	t.Parallel()

	// A representative SES-style tracked URL with a signed token and a
	// fragment — both must be absent from every output.
	const rawLink = "https://click.example.com/r?token=s3cr3t&uid=abc123#section"
	const wantLink = "https://click.example.com/r"

	store := mocks.NewTrackingStore()
	seedRecord(store)
	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	msg := makeClickSNSMsg(t, "sns-redact-1", rawLink, testEmailID, testGroupID, testTimestamp)
	require.NoError(t, h.Handle(context.Background(), msg))

	// 1. KV record must store the redacted link.
	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok, "record must be persisted")
	require.Len(t, record.ClickList, 1, "one ClickList entry expected")
	assert.Equal(t, wantLink, record.ClickList[0].Link,
		"stored ClickList link must have query and fragment stripped")
	assert.NotContains(t, record.ClickList[0].Link, "s3cr3t",
		"token must not appear in stored link")

	// 2. NATS publish payload must also carry the redacted link.
	require.Len(t, pub.calls, 1, "exactly one NATS publish expected")
	var published api.EmailLinkClickedEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &published))
	assert.Equal(t, wantLink, published.Link,
		"published link must have query and fragment stripped")
	assert.NotContains(t, published.Link, "s3cr3t",
		"token must not appear in published link")
}

// TestEngagementEventHandler_Click_LinkRedaction_RelativeURLFallback covers
// the fail-closed fallback path: a relative or host-less URL must have its
// query stripped even though url.Parse cannot return a usable Host.
func TestEngagementEventHandler_Click_LinkRedaction_RelativeURLFallback(t *testing.T) {
	t.Parallel()

	const rawLink = "/track/open?token=leaked&ref=email"
	const wantLink = "/track/open"

	store := mocks.NewTrackingStore()
	seedRecord(store)
	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	msg := makeClickSNSMsg(t, "sns-redact-relative", rawLink, testEmailID, testGroupID, testTimestamp)
	require.NoError(t, h.Handle(context.Background(), msg))

	// KV record must store the redacted link.
	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)
	require.Len(t, record.ClickList, 1)
	assert.Equal(t, wantLink, record.ClickList[0].Link,
		"fallback redaction must strip query from relative URL")
	assert.NotContains(t, record.ClickList[0].Link, "leaked")

	// NATS payload must also be redacted.
	require.Len(t, pub.calls, 1)
	var published api.EmailLinkClickedEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &published))
	assert.Equal(t, wantLink, published.Link,
		"fallback redaction must strip query from published relative URL")
	assert.NotContains(t, published.Link, "leaked")
}

// ---------------------------------------------------------------------------
// Single-fire replay tests
//
// DELIVERY, BOUNCE, and COMPLAINT are idempotent booleans: once the
// corresponding flag is set, a replay of the same SQS message must not
// emit a second NATS push. This is the publish counterpart to the KV
// idempotency that the handler enforces via the eventApplied return value.
// ---------------------------------------------------------------------------

// TestEngagementEventHandler_Handle_Deduplication_SingleFireEvents verifies
// that replaying a DELIVERY, BOUNCE, or COMPLAINT SQS message does not produce
// a second NATS publish. The event is applied on the first delivery and silently
// dropped on the second because applyEngagementEvent returns false.
func TestEngagementEventHandler_Handle_Deduplication_SingleFireEvents(t *testing.T) {
	t.Parallel()

	for _, eventType := range []string{"DELIVERY", "BOUNCE", "COMPLAINT"} {
		eventType := eventType
		t.Run(eventType, func(t *testing.T) {
			t.Parallel()

			store := mocks.NewTrackingStore()
			seedRecord(store)
			pub := &mockPublisher{}
			h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

			msg := sqsMsg(t, eventType, testEmailID, testGroupID, testTimestamp)

			// First delivery: event is applied, push is emitted.
			require.NoError(t, h.Handle(context.Background(), msg))
			require.Len(t, pub.calls, 1, "first delivery must emit exactly one push")

			// SQS replay: boolean is already set; applyEngagementEvent returns
			// false so the handler must not publish a second notification.
			require.NoError(t, h.Handle(context.Background(), msg))
			assert.Len(t, pub.calls, 1,
				"replay of %s must not emit a second NATS push", eventType)
		})
	}
}

// ---------------------------------------------------------------------------
// Out-of-order OPEN timestamp test
//
// SQS is at-least-once and does not guarantee ordering. When an OPEN arrives
// with a timestamp earlier than a previously recorded open, the published
// EmailOpenedEvent.OpenedAt must reflect the SES sub-object timestamp, not
// the aggregate LastOpenedAt. This verifies the handler captures the per-event
// timestamp before the KV write rather than reading it back from the record.
// ---------------------------------------------------------------------------

// TestEngagementEventHandler_Handle_Open_OutOfOrderTimestamp verifies that the
// published opened_at value comes from the current SES event, not from
// record.LastOpenedAt. Seed the record with OpenCount=1, LastOpenedAt=T2, then
// handle an OPEN with a new MessageId and timestamp T1 where T1 < T2.
// The push must carry T1 and OpenCount must be incremented to 2.
func TestEngagementEventHandler_Handle_Open_OutOfOrderTimestamp(t *testing.T) {
	t.Parallel()

	const (
		t1 = "2026-01-01T10:00:00Z" // earlier — this is the new event's timestamp
		t2 = "2026-01-02T12:00:00Z" // later   — already recorded as LastOpenedAt
	)

	t2Parsed := mustParseTime(t, t2)

	store := mocks.NewTrackingStore()
	// Seed a record that already has one open at T2 so LastOpenedAt is set.
	existingEventID := "sns-msg-existing"
	store.PutRecord(testEmailID, api.EmailRecipientRecord{
		EmailID:      testEmailID,
		GroupID:      testGroupID,
		Opened:       true,
		OpenCount:    1,
		LastOpenedAt: &t2Parsed,
		OpenedAtList: []api.OpenEvent{
			{EventID: existingEventID, OpenedAt: t2Parsed},
		},
	})

	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	// Construct an OPEN with a new MessageId and the earlier timestamp T1.
	msg := sqsMsg(t, "Open", testEmailID, testGroupID, t1, func(m map[string]any) {
		m["open"] = map[string]any{"timestamp": t1, "ipAddress": "1.2.3.4", "userAgent": "Mozilla/5.0"}
	})
	// Override the SNS MessageId so it is distinct from the seed.
	body := *msg.Body
	body = strings.Replace(body, "sns-msg-1", "sns-msg-new", 1)
	msg.Body = &body

	require.NoError(t, h.Handle(context.Background(), msg))

	require.Len(t, pub.calls, 1)
	var evt api.EmailOpenedEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &evt))

	// The published timestamp must be T1 (the SES event timestamp), not T2.
	assert.Equal(t, mustParseTime(t, t1), evt.OpenedAt,
		"published opened_at must come from the current SES event, not LastOpenedAt")
	// Open count must have incremented to 2.
	assert.Equal(t, 2, evt.OpenCount,
		"open_count must reflect the incremented value after the new event")
}
