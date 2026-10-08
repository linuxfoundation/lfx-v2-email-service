// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
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
	testEmailID   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	testGroupID   = "11111111-1111-1111-1111-111111111111"
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
	// EventID must carry the SNS MessageId so consumers can deduplicate clicks
	// once the server-side bounded dedup window is exhausted.
	assert.Equal(t, "sns-msg-1", evt.EventID)
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

	// Use a valid UUID that is not seeded in the store so the handler reaches
	// the UpdateRecord "not found" path (after UUID validation passes).
	missingEmailID := "99999999-9999-9999-9999-999999999999"
	for _, eventType := range []string{"BOUNCE", "DELIVERY", "Open", "Click"} {
		msg := sqsMsg(t, eventType, missingEmailID, testGroupID, testTimestamp)
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
// entry and the dedup ID are rolled back. ClickCount and LastClickedAt are still
// recorded (they are contract scalars, not compactable history), and the published
// clicked_at matches LastClickedAt. Only ClickList and ClickEventIDs are rolled back.
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
	// LastClickedAt must still be set: it is a contract field (not optional
	// history) and must stay in sync with ClickCount and the published event.
	require.NotNil(t, record.LastClickedAt, "LastClickedAt must be set even when history/dedup are rolled back")
	wantAt := mustParseTime(t, testTimestamp)
	assert.Equal(t, wantAt, *record.LastClickedAt, "LastClickedAt must equal the event timestamp")

	// The handler still publishes the counted click.
	require.Len(t, pub.calls, 1)
	assert.Equal(t, api.EmailLinkClickedSubject, pub.calls[0].subject)
	var clickEvt api.EmailLinkClickedEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &clickEvt))
	assert.Equal(t, wantAt, clickEvt.ClickedAt, "published clicked_at must equal LastClickedAt in KV record")
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

// TestEngagementEventHandler_Handle_MalformedTimestamp_KVMatchesPublished verifies
// that when the SES event sub-object has an empty or non-RFC3339 timestamp,
// the fallback time.Now() value written to the KV record and the timestamp
// in the published NATS event are identical. Before the applyEngagementEvent
// return-value fix a second call to parseTimestamp on the publish path would
// produce a different time.Now() instant.
func TestEngagementEventHandler_Handle_MalformedTimestamp_KVMatchesPublished(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		msg         func(store *mocks.TrackingStore) types.Message
		storedAt    func(record api.EmailRecipientRecord) *time.Time
		publishedAt func(t *testing.T, data []byte) time.Time
	}{
		{
			name: "DELIVERY_empty_timestamp",
			msg: func(store *mocks.TrackingStore) types.Message {
				seedRecord(store)
				return sqsMsg(t, "DELIVERY", testEmailID, testGroupID, "")
			},
			storedAt: func(r api.EmailRecipientRecord) *time.Time { return r.DeliveredAt },
			publishedAt: func(t *testing.T, data []byte) time.Time {
				var e api.EmailDeliveredEvent
				require.NoError(t, json.Unmarshal(data, &e))
				return e.DeliveredAt
			},
		},
		{
			name: "OPEN_empty_timestamp",
			msg: func(store *mocks.TrackingStore) types.Message {
				seedRecord(store)
				return sqsMsg(t, "Open", testEmailID, testGroupID, "")
			},
			storedAt: func(r api.EmailRecipientRecord) *time.Time {
				if len(r.OpenedAtList) == 0 {
					return nil
				}
				t := r.OpenedAtList[0].OpenedAt
				return &t
			},
			publishedAt: func(t *testing.T, data []byte) time.Time {
				var e api.EmailOpenedEvent
				require.NoError(t, json.Unmarshal(data, &e))
				return e.OpenedAt
			},
		},
		{
			name: "CLICK_empty_timestamp",
			msg: func(store *mocks.TrackingStore) types.Message {
				seedRecord(store)
				return sqsMsg(t, "Click", testEmailID, testGroupID, "")
			},
			storedAt: func(r api.EmailRecipientRecord) *time.Time {
				if len(r.ClickList) == 0 {
					return nil
				}
				t := r.ClickList[0].ClickedAt
				return &t
			},
			publishedAt: func(t *testing.T, data []byte) time.Time {
				var e api.EmailLinkClickedEvent
				require.NoError(t, json.Unmarshal(data, &e))
				return e.ClickedAt
			},
		},
		{
			name: "BOUNCE_empty_timestamp",
			msg: func(store *mocks.TrackingStore) types.Message {
				seedRecord(store)
				return sqsMsg(t, "BOUNCE", testEmailID, testGroupID, "")
			},
			storedAt: func(r api.EmailRecipientRecord) *time.Time { return r.FailedAt },
			publishedAt: func(t *testing.T, data []byte) time.Time {
				var e api.EmailFailedEvent
				require.NoError(t, json.Unmarshal(data, &e))
				return e.FailedAt
			},
		},
		{
			name: "COMPLAINT_empty_timestamp",
			msg: func(store *mocks.TrackingStore) types.Message {
				seedRecord(store)
				return sqsMsg(t, "COMPLAINT", testEmailID, testGroupID, "")
			},
			storedAt: func(r api.EmailRecipientRecord) *time.Time { return r.FailedAt },
			publishedAt: func(t *testing.T, data []byte) time.Time {
				var e api.EmailFailedEvent
				require.NoError(t, json.Unmarshal(data, &e))
				return e.FailedAt
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := mocks.NewTrackingStore()
			msg := tc.msg(store)
			pub := &mockPublisher{}
			h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

			require.NoError(t, h.Handle(context.Background(), msg))

			record, ok := store.GetStoredRecord(testEmailID)
			require.True(t, ok)

			require.Len(t, pub.calls, 1, "expected exactly one publish")
			storedAt := tc.storedAt(record)
			require.NotNil(t, storedAt, "stored timestamp field must be set")
			publishedAt := tc.publishedAt(t, pub.calls[0].data)

			assert.Equal(t, *storedAt, publishedAt,
				"KV stored timestamp and published event timestamp must be identical "+
					"even when SES omits or malforms the event timestamp")
		})
	}
}

// TestEngagementEventHandler_Handle_NonUUIDTrackingID verifies that SES events
// whose extracted email_id segment (the part after the last '/' in
// X-LFX-TRACKING-ID) is not a UUID are dropped before any KV operation, so an
// adversarially large value cannot trigger a NATS max_control_line violation and
// close the service's shared connection.
//
// The invariant is tested by injecting an error into GetErrFor for the extracted
// email_id: if the UUID guard is bypassed and UpdateRecord is called, Handle
// returns a non-nil error, causing require.NoError to fail.
func TestEngagementEventHandler_Handle_NonUUIDTrackingID(t *testing.T) {
	t.Parallel()

	buildMsg := func(trackingID string) types.Message {
		sesMsg := map[string]any{
			"eventType": "BOUNCE",
			"mail": map[string]any{
				"headers": []map[string]any{
					{"name": "X-LFX-TRACKING-ID", "value": trackingID},
				},
			},
			"bounce": map[string]any{"timestamp": testTimestamp},
		}
		inner, _ := json.Marshal(sesMsg)
		outer, _ := json.Marshal(map[string]string{"MessageId": "sns-x", "Message": string(inner)})
		body := string(outer)
		return types.Message{Body: &body}
	}

	// extractedFrom returns the email_id that extractEmailID would pull from a
	// tracking header value (everything after the last '/').
	extractedFrom := func(trackingID string) string {
		if idx := strings.LastIndex(trackingID, "/"); idx != -1 {
			return trackingID[idx+1:]
		}
		return trackingID
	}

	cases := []struct {
		name       string
		trackingID string
	}{
		{"short non-UUID", "not-a-uuid"},
		// 300 chars: far over the UUID length but small enough for a map key.
		{"oversized (300 chars)", strings.Repeat("a", 300)},
		{"UUID-format group slash oversized email", testGroupID + "/" + strings.Repeat("b", 300)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := mocks.NewTrackingStore()
			// Inject an error for the extracted email_id so that any KV access
			// (UpdateRecord → GetErrFor lookup) makes Handle return non-nil.
			// If the UUID guard is removed, require.NoError catches the bypass.
			store.GetErrFor = map[string]error{
				extractedFrom(tc.trackingID): errors.New("KV must not be reached for non-UUID tracking id"),
			}
			pub := &mockPublisher{}
			h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

			require.NoError(t, h.Handle(context.Background(), buildMsg(tc.trackingID)))
			assert.Empty(t, pub.calls, "must not publish when tracking id is not a UUID")
		})
	}
}

// makeOpenSNSMsg builds an SQS message wrapping an Open SES event with a
// caller-supplied SNS MessageId.
func makeOpenSNSMsg(t *testing.T, snsID, emailID, groupID, timestamp string) types.Message {
	t.Helper()
	sesMsg := map[string]any{
		"eventType": "Open",
		"mail": map[string]any{
			"headers": []map[string]any{
				{"name": "X-LFX-TRACKING-ID", "value": groupID + "/" + emailID},
			},
		},
		"open": map[string]any{"timestamp": timestamp},
	}
	inner, err := json.Marshal(sesMsg)
	require.NoError(t, err)
	outer, err := json.Marshal(map[string]string{"MessageId": snsID, "Message": string(inner)})
	require.NoError(t, err)
	body := string(outer)
	return types.Message{Body: &body}
}

// TestEngagementEventHandler_Open_RecordStaysWithinKVLimit verifies that an
// unbounded stream of unique OPEN events (e.g. repeated tracking-pixel loads)
// cannot grow the record past the email-recipients bucket's 65 536-byte
// maxValueSize, and that later OPEN, DELIVERY, and BOUNCE events still apply.
func TestEngagementEventHandler_Open_RecordStaysWithinKVLimit(t *testing.T) {
	t.Parallel()

	const opens = 1_200

	store := mocks.NewTrackingStore()
	store.PutRecord(testEmailID, api.EmailRecipientRecord{
		EmailID: testEmailID,
		GroupID: testGroupID,
		To:      "recipient@example.com",
		Subject: strings.Repeat("s", 500),
	})

	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	for i := range opens {
		snsID := fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
		require.NoError(t, h.Handle(context.Background(), makeOpenSNSMsg(t, snsID, testEmailID, testGroupID, testTimestamp)))
	}

	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)
	assert.Equal(t, opens, record.OpenCount, "every unique open must be counted")
	assert.LessOrEqual(t, len(record.OpenedAtList), 500, "OpenedAtList must be capped")
	b, err := json.Marshal(record)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(b), 50_000, "serialised record must stay within the soft ceiling")
	assert.Less(t, len(b), 65_536, "serialised record must stay within the KV bucket hard limit")

	// Later events still apply after the open window is exhausted.
	require.NoError(t, h.Handle(context.Background(), makeOpenSNSMsg(t, "ffffffff-ffff-4fff-8fff-ffffffffffff", testEmailID, testGroupID, testTimestamp)))
	require.NoError(t, h.Handle(context.Background(), sqsMsg(t, "DELIVERY", testEmailID, testGroupID, testTimestamp)))
	require.NoError(t, h.Handle(context.Background(), sqsMsg(t, "BOUNCE", testEmailID, testGroupID, testTimestamp)))

	record, ok = store.GetStoredRecord(testEmailID)
	require.True(t, ok)
	assert.Equal(t, opens+1, record.OpenCount)
	assert.True(t, record.Delivered, "DELIVERY must apply after the open window is exhausted")
	assert.True(t, record.Failed, "BOUNCE must apply after the open window is exhausted")
	b, err = json.Marshal(record)
	require.NoError(t, err)
	assert.Less(t, len(b), 65_536, "serialised record must stay within the KV bucket hard limit")

	require.Len(t, pub.calls, opens+3)
	assert.Equal(t, api.EmailFailedSubject, pub.calls[len(pub.calls)-1].subject)
}

// TestEngagementEventHandler_Open_EntryRolledBackAtSizeLimit verifies that an
// OPEN entry that would push the record past the 50 KB soft ceiling is not
// stored, while the open is still counted and published.
func TestEngagementEventHandler_Open_EntryRolledBackAtSizeLimit(t *testing.T) {
	t.Parallel()

	store := mocks.NewTrackingStore()
	store.PutRecord(testEmailID, api.EmailRecipientRecord{
		EmailID: testEmailID,
		GroupID: testGroupID,
		Subject: strings.Repeat("x", 49_800),
	})

	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

	require.NoError(t, h.Handle(context.Background(), makeOpenSNSMsg(t, "sns-open-overflow", testEmailID, testGroupID, testTimestamp)))

	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)
	assert.True(t, record.Opened)
	assert.Equal(t, 1, record.OpenCount, "open must be counted")
	assert.Empty(t, record.OpenedAtList, "OpenedAtList entry must be rolled back to keep the record within the KV limit")
	require.NotNil(t, record.LastOpenedAt)
	assert.Equal(t, mustParseTime(t, testTimestamp), *record.LastOpenedAt)

	require.Len(t, pub.calls, 1)
	var evt api.EmailOpenedEvent
	require.NoError(t, json.Unmarshal(pub.calls[0].data, &evt))
	assert.Equal(t, 1, evt.OpenCount)
}

// TestEngagementEventHandler_Handle_RecordTooLarge_NotRetried verifies that a
// deterministic size rejection from the store is acknowledged (Handle returns
// nil, no publish) while any other store error remains retryable.
func TestEngagementEventHandler_Handle_RecordTooLarge_NotRetried(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		storeErr  error
		wantError bool
	}{
		{"record too large", fmt.Errorf("kv update: %w", domain.ErrRecordTooLarge), false},
		{"transient error", errors.New("kv unavailable"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := mocks.NewTrackingStore()
			seedRecord(store)
			store.GetErrFor = map[string]error{testEmailID: tc.storeErr}
			pub := &mockPublisher{}
			h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)

			err := h.Handle(context.Background(), sqsMsg(t, "BOUNCE", testEmailID, testGroupID, testTimestamp))
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Empty(t, pub.calls, "must not publish when the KV write failed")
		})
	}
}

// TestEngagementEventHandler_Open_LegacyOversizedRecordCompacted verifies that a
// record whose OpenedAtList grew past the bound before it was enforced is
// trimmed to its newest entries on the next event, so the event is written and
// the record returns within the KV size budget without losing OpenCount.
func TestEngagementEventHandler_Open_LegacyOversizedRecordCompacted(t *testing.T) {
	t.Parallel()

	const legacyOpens = 720
	base := mustParseTime(t, testTimestamp)
	list := make([]api.OpenEvent, legacyOpens)
	for i := range list {
		list[i] = api.OpenEvent{EventID: fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i), OpenedAt: base.Add(time.Duration(i) * time.Second)}
	}
	last := list[legacyOpens-1].OpenedAt

	store := mocks.NewTrackingStore()
	store.PutRecord(testEmailID, api.EmailRecipientRecord{
		EmailID:      testEmailID,
		GroupID:      testGroupID,
		To:           "recipient@example.com",
		Subject:      strings.Repeat("s", 500),
		Opened:       true,
		OpenCount:    legacyOpens,
		OpenedAtList: list,
		LastOpenedAt: &last,
	})
	seeded, _ := store.GetStoredRecord(testEmailID)
	b, err := json.Marshal(seeded)
	require.NoError(t, err)
	require.Greater(t, len(b), 60_000, "seeded legacy record must be near the KV bucket hard limit")

	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)
	require.NoError(t, h.Handle(context.Background(), sqsMsg(t, "BOUNCE", testEmailID, testGroupID, testTimestamp)))

	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)
	assert.True(t, record.Failed, "BOUNCE must apply to a compacted legacy record")
	assert.Equal(t, legacyOpens, record.OpenCount, "compaction must not change OpenCount")
	require.NotEmpty(t, record.OpenedAtList)
	assert.LessOrEqual(t, len(record.OpenedAtList), 500)
	assert.Equal(t, list[legacyOpens-1].EventID, record.OpenedAtList[len(record.OpenedAtList)-1].EventID,
		"newest open entries must be retained")
	b, err = json.Marshal(record)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(b), 50_000, "compacted record must be within the soft ceiling")
	require.Len(t, pub.calls, 1)
	assert.Equal(t, api.EmailFailedSubject, pub.calls[0].subject)
}

// TestEngagementEventHandler_Open_CountNotBelowListLength verifies that a
// record whose open_count lags its stored OpenedAtList (written before
// open_count existed) continues counting from the list length.
func TestEngagementEventHandler_Open_CountNotBelowListLength(t *testing.T) {
	t.Parallel()

	store := mocks.NewTrackingStore()
	store.PutRecord(testEmailID, api.EmailRecipientRecord{
		EmailID: testEmailID,
		GroupID: testGroupID,
		Opened:  true,
		OpenedAtList: []api.OpenEvent{
			{EventID: "sns-old-1", OpenedAt: mustParseTime(t, testTimestamp)},
			{EventID: "sns-old-2", OpenedAt: mustParseTime(t, testTimestamp)},
		},
	})

	h := service.NewEngagementEventHandler(store)
	require.NoError(t, h.Handle(context.Background(), makeOpenSNSMsg(t, "sns-new", testEmailID, testGroupID, testTimestamp)))

	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)
	assert.Equal(t, 3, record.OpenCount)
	assert.Len(t, record.OpenedAtList, 3)
}

// seedLegacyOpens stores a record holding opens entries in OpenedAtList, with
// the subject padded so the serialised record is targetBytes long.
func seedLegacyOpens(t *testing.T, store *mocks.TrackingStore, opens, targetBytes int) []api.OpenEvent {
	t.Helper()
	base := mustParseTime(t, testTimestamp)
	list := make([]api.OpenEvent, opens)
	for i := range list {
		list[i] = api.OpenEvent{EventID: fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i), OpenedAt: base.Add(time.Duration(i) * time.Second)}
	}
	last := list[opens-1].OpenedAt
	r := api.EmailRecipientRecord{
		EmailID:      testEmailID,
		GroupID:      testGroupID,
		To:           "recipient@example.com",
		Opened:       true,
		OpenCount:    opens,
		OpenedAtList: list,
		LastOpenedAt: &last,
	}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	require.Less(t, len(b), targetBytes)
	r.Subject = strings.Repeat("s", targetBytes-len(b))
	b, err = json.Marshal(r)
	require.NoError(t, err)
	require.Equal(t, targetBytes, len(b))
	store.PutRecord(testEmailID, r)
	return list
}

// TestEngagementEventHandler_Open_LegacyNearLimitRecordCompacted verifies that
// a record inflated before OpenedAtList was bounded, but holding no more than
// maxOpenEvents entries, is still trimmed when it is near the KV bucket hard
// limit, so a later status event is written rather than rejected for size.
func TestEngagementEventHandler_Open_LegacyNearLimitRecordCompacted(t *testing.T) {
	t.Parallel()

	const legacyOpens = 450
	store := mocks.NewTrackingStore()
	list := seedLegacyOpens(t, store, legacyOpens, 64_000)

	pub := &mockPublisher{}
	h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)
	require.NoError(t, h.Handle(context.Background(), sqsMsg(t, "BOUNCE", testEmailID, testGroupID, testTimestamp)))

	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)
	assert.True(t, record.Failed, "BOUNCE must apply to a compacted legacy record")
	assert.Equal(t, legacyOpens, record.OpenCount, "compaction must not lower OpenCount")
	require.NotEmpty(t, record.OpenedAtList)
	assert.Less(t, len(record.OpenedAtList), legacyOpens, "OpenedAtList must be trimmed")
	assert.Equal(t, list[legacyOpens-1].EventID, record.OpenedAtList[len(record.OpenedAtList)-1].EventID,
		"newest open entries must be retained")
	b, err := json.Marshal(record)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(b), 50_000, "compacted record must be within the soft ceiling")
	require.Len(t, pub.calls, 1)
	assert.Equal(t, api.EmailFailedSubject, pub.calls[0].subject)
}

// TestEngagementEventHandler_Open_RecordNearSoftCeilingNotCompacted verifies
// that a record slightly above the 50 KB soft ceiling (as fixed-size fields
// can leave a current record) keeps its open history, so compaction does not
// repeatedly trim records written by the bounded code path.
func TestEngagementEventHandler_Open_RecordNearSoftCeilingNotCompacted(t *testing.T) {
	t.Parallel()

	const opens = 300
	store := mocks.NewTrackingStore()
	seedLegacyOpens(t, store, opens, 52_000)

	h := service.NewEngagementEventHandler(store)
	require.NoError(t, h.Handle(context.Background(), sqsMsg(t, "DELIVERY", testEmailID, testGroupID, testTimestamp)))

	record, ok := store.GetStoredRecord(testEmailID)
	require.True(t, ok)
	assert.True(t, record.Delivered)
	assert.Len(t, record.OpenedAtList, opens, "records under the compaction threshold must not be trimmed")
	assert.Equal(t, opens, record.OpenCount)
}

// TestEngagementEventHandler_Open_ReplayOfTrimmableEntryDeduplicated verifies
// that an OPEN replay whose MessageId is among the oldest stored entries (the
// ones compaction would trim) is still recognised as a duplicate, for both
// compaction triggers.
func TestEngagementEventHandler_Open_ReplayOfTrimmableEntryDeduplicated(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		opens       int
		targetBytes int
	}{
		{"over entry cap", 720, 65_000},
		{"over size threshold under entry cap", 450, 64_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := mocks.NewTrackingStore()
			list := seedLegacyOpens(t, store, tc.opens, tc.targetBytes)

			pub := &mockPublisher{}
			h := service.NewEngagementEventHandler(store).WithEngagementPublisher(pub)
			require.NoError(t, h.Handle(context.Background(), makeOpenSNSMsg(t, list[0].EventID, testEmailID, testGroupID, testTimestamp)))

			record, ok := store.GetStoredRecord(testEmailID)
			require.True(t, ok)
			assert.Equal(t, tc.opens, record.OpenCount, "a replayed OPEN must not be counted")
			assert.Empty(t, pub.calls, "a replayed OPEN must not be published")
		})
	}
}
