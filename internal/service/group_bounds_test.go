// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service/mocks"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

const boundsGroup = "grp_0123456789abcdef0123456789abcdef"

// seedBigGroup records n emails, each with a readable record, under boundsGroup.
func seedBigGroup(t *testing.T, store *mocks.TrackingStore, n int) []string {
	t.Helper()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		store.PutRecord(ids[i], api.EmailRecipientRecord{EmailID: ids[i], GroupID: boundsGroup, Delivered: true})
	}
	store.PutGroup(boundsGroup, ids)
	return ids
}

func call(ctx context.Context, t *testing.T, handle func(context.Context, []byte, func([]byte) error), req any) []byte {
	t.Helper()
	data, err := json.Marshal(req)
	require.NoError(t, err)
	var resp []byte
	calls := 0
	handle(ctx, data, func(d []byte) error { resp = d; calls++; return nil })
	require.Equal(t, 1, calls, "respond must be called exactly once")
	return resp
}

func errorOf(t *testing.T, resp []byte) string {
	t.Helper()
	var e api.SendEmailErrorResponse
	require.NoError(t, json.Unmarshal(resp, &e))
	return e.Error
}

func TestGetEmailStatusHandler_GroupPaging(t *testing.T) {
	t.Parallel()
	store := mocks.NewTrackingStore()
	ids := seedBigGroup(t, store, api.MaxGroupStatusLimit+250)
	h := service.NewGetEmailStatusHandler(store)

	pageIDs := func(resp []byte) []string {
		var recs []api.EmailRecipientRecord
		require.NoError(t, json.Unmarshal(resp, &recs), string(resp))
		out := make([]string, len(recs))
		for i, r := range recs {
			out[i] = r.EmailID
		}
		return out
	}

	t.Run("default limit", func(t *testing.T) {
		t.Parallel()
		got := pageIDs(call(context.Background(), t, h.HandleData, api.GetEmailStatusRequest{GroupID: boundsGroup}))
		assert.Equal(t, ids[:api.DefaultGroupStatusLimit], got)
	})

	t.Run("offset and limit", func(t *testing.T) {
		t.Parallel()
		got := pageIDs(call(context.Background(), t, h.HandleData, api.GetEmailStatusRequest{GroupID: boundsGroup, Offset: 1000, Limit: 400}))
		assert.Equal(t, ids[1000:], got, "last page is short")
	})

	t.Run("offset past end returns empty page", func(t *testing.T) {
		t.Parallel()
		resp := call(context.Background(), t, h.HandleData, api.GetEmailStatusRequest{GroupID: boundsGroup, Offset: len(ids)})
		assert.JSONEq(t, `[]`, string(resp))
	})

	for name, req := range map[string]api.GetEmailStatusRequest{
		"negative offset": {GroupID: boundsGroup, Offset: -1},
		"negative limit":  {GroupID: boundsGroup, Limit: -1},
		"limit too large": {GroupID: boundsGroup, Limit: api.MaxGroupStatusLimit + 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			msg := errorOf(t, call(context.Background(), t, h.HandleData, req))
			assert.True(t, strings.HasPrefix(msg, "invalid "), msg)
		})
	}
}

func TestGetEmailStatusHandler_GroupReplyTooLarge(t *testing.T) {
	t.Parallel()
	store := mocks.NewTrackingStore()
	seedBigGroup(t, store, 100)
	h := service.NewGetEmailStatusHandler(store).WithMaxPayload(2048)

	resp := call(context.Background(), t, h.HandleData, api.GetEmailStatusRequest{GroupID: boundsGroup, Limit: 100})
	assert.Equal(t, "response too large", errorOf(t, resp))

	// A page that fits is still served under the same limit.
	resp = call(context.Background(), t, h.HandleData, api.GetEmailStatusRequest{GroupID: boundsGroup, Limit: 2})
	var recs []api.EmailRecipientRecord
	require.NoError(t, json.Unmarshal(resp, &recs))
	assert.Len(t, recs, 2)
	assert.LessOrEqual(t, len(resp), 2048)

	// The size check is exact: a page of exactly maxPayload bytes is sent, one
	// byte less is refused.
	exact := int64(len(resp))
	resp = call(context.Background(), t, service.NewGetEmailStatusHandler(store).WithMaxPayload(exact).HandleData, api.GetEmailStatusRequest{GroupID: boundsGroup, Limit: 2})
	require.NoError(t, json.Unmarshal(resp, &recs))
	assert.Len(t, recs, 2)
	resp = call(context.Background(), t, service.NewGetEmailStatusHandler(store).WithMaxPayload(exact-1).HandleData, api.GetEmailStatusRequest{GroupID: boundsGroup, Limit: 2})
	assert.Equal(t, "response too large", errorOf(t, resp))
}

func TestGroupReadHandlers_Deadline(t *testing.T) {
	t.Parallel()
	store := mocks.NewTrackingStore()
	seedBigGroup(t, store, 10)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	resp := call(ctx, t, service.NewGetEmailStatusHandler(store).HandleData, api.GetEmailStatusRequest{GroupID: boundsGroup})
	assert.Equal(t, "timeout", errorOf(t, resp))

	// An empty window still reports the expired deadline, as kv.Store does.
	resp = call(ctx, t, service.NewGetEmailStatusHandler(store).HandleData, api.GetEmailStatusRequest{GroupID: boundsGroup, Offset: 10})
	assert.Equal(t, "timeout", errorOf(t, resp))

	resp = call(ctx, t, service.NewGetEmailEngagementAnalyticsHandler(store).HandleData, api.GetEmailEngagementAnalyticsRequest{GroupID: boundsGroup})
	assert.Equal(t, "timeout", errorOf(t, resp))
}

func TestGetEmailEngagementAnalyticsHandler_FullGroup(t *testing.T) {
	t.Parallel()
	store := mocks.NewTrackingStore()
	seedBigGroup(t, store, api.MaxGroupEmails)

	resp := call(context.Background(), t, service.NewGetEmailEngagementAnalyticsHandler(store).HandleData, api.GetEmailEngagementAnalyticsRequest{GroupID: boundsGroup})
	var got api.GetEmailEngagementAnalyticsResponse
	require.NoError(t, json.Unmarshal(resp, &got), string(resp))
	assert.Equal(t, api.MaxGroupEmails, got.TotalSent)
	assert.Equal(t, api.MaxGroupEmails, got.Delivered)
}

func TestSendEmailHandler_FullGroup(t *testing.T) {
	t.Parallel()
	policy := domain.NewAddressPolicy([]string{"lfx.linuxfoundation.org"}, []string{"linuxfoundation.org"}, nil)
	req := api.SendEmailRequest{To: "a@example.com", Subject: "Hi", HTML: "<p>Hi</p>", Text: "Hi", GroupID: boundsGroup}

	t.Run("rejected before sending", func(t *testing.T) {
		t.Parallel()
		store := mocks.NewTrackingStore()
		seedBigGroup(t, store, api.MaxGroupEmails)
		sender := &mockSender{emailID: "11111111-1111-4111-8111-111111111111", groupID: boundsGroup}

		resp := call(context.Background(), t, service.NewSendEmailHandler(sender, store, policy).HandleData, req)
		assert.Equal(t, "group is full", errorOf(t, resp))
		assert.False(t, sender.called, "no mail is sent to a full group")
		ids, _ := store.GetStoredGroup(boundsGroup)
		assert.Len(t, ids, api.MaxGroupEmails)
	})

	t.Run("group filled after the check: handle withheld, no record", func(t *testing.T) {
		t.Parallel()
		store := mocks.NewTrackingStore()
		store.PutGroup(boundsGroup, []string{"00000000-0000-4000-8000-000000000000"})
		store.AppendErr = fmt.Errorf("append: %w", domain.ErrGroupFull)
		const emailID = "11111111-1111-4111-8111-111111111111"
		sender := &mockSender{emailID: emailID, groupID: boundsGroup}

		resp := call(context.Background(), t, service.NewSendEmailHandler(sender, store, policy).HandleData, req)
		var got api.SendEmailResponse
		require.NoError(t, json.Unmarshal(resp, &got))
		assert.True(t, sender.called)
		assert.Equal(t, emailID, got.EmailID)
		assert.Empty(t, got.GroupID, "a handle the email is not tracked under must not be returned")
		_, ok := store.GetStoredRecord(emailID)
		assert.False(t, ok)
	})
}
