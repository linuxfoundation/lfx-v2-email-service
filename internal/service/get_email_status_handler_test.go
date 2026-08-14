// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service/mocks"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

func seedRecipient(t *testing.T, store *mocks.TrackingStore, emailID, groupID string) api.EmailRecipientRecord {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	record := api.EmailRecipientRecord{
		EmailID: emailID,
		GroupID: groupID,
		To:      "user@example.com",
		Subject: "Hello",
		SentAt:  now,
	}
	store.PutRecord(emailID, record)
	return record
}

func seedGroupIndex(t *testing.T, store *mocks.TrackingStore, groupID string, emailIDs []string) {
	t.Helper()
	store.PutGroup(groupID, emailIDs)
}

func TestGetEmailStatusHandler_HandleData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		payload     any
		setup       func(store *mocks.TrackingStore)
		wantErrMsg  string
		wantRecord  *api.EmailRecipientRecord
		wantRecords *[]api.EmailRecipientRecord
	}{
		{
			name:       "malformed JSON",
			payload:    "{not json",
			wantErrMsg: "invalid request payload",
		},
		{
			name:       "neither email_id nor group_id",
			payload:    api.GetEmailStatusRequest{},
			wantErrMsg: "email_id or group_id is required",
		},
		{
			name:       "both email_id and group_id",
			payload:    api.GetEmailStatusRequest{EmailID: "abc", GroupID: "grp"},
			wantErrMsg: "only one of email_id or group_id may be set",
		},
		{
			name:    "email_id happy path",
			payload: api.GetEmailStatusRequest{EmailID: "email-1"},
			setup: func(store *mocks.TrackingStore) {
				seedRecipient(t, store, "email-1", "group-1")
			},
			wantRecord: &api.EmailRecipientRecord{EmailID: "email-1", GroupID: "group-1", To: "user@example.com", Subject: "Hello"},
		},
		{
			name:       "email_id not found",
			payload:    api.GetEmailStatusRequest{EmailID: "missing"},
			wantErrMsg: "not found",
		},
		{
			name:    "email_id KV internal error",
			payload: api.GetEmailStatusRequest{EmailID: "bad-key"},
			setup: func(store *mocks.TrackingStore) {
				store.GetErrFor = map[string]error{"bad-key": errors.New("kv unavailable")}
			},
			wantErrMsg: "internal error",
		},
		{
			name:    "group_id happy path",
			payload: api.GetEmailStatusRequest{GroupID: "grp-a"},
			setup: func(store *mocks.TrackingStore) {
				seedRecipient(t, store, "e1", "grp-a")
				seedRecipient(t, store, "e2", "grp-a")
				seedGroupIndex(t, store, "grp-a", []string{"e1", "e2"})
			},
			wantRecords: &[]api.EmailRecipientRecord{
				{EmailID: "e1", GroupID: "grp-a", To: "user@example.com", Subject: "Hello"},
				{EmailID: "e2", GroupID: "grp-a", To: "user@example.com", Subject: "Hello"},
			},
		},
		{
			name:       "group_id not found",
			payload:    api.GetEmailStatusRequest{GroupID: "missing-grp"},
			wantErrMsg: "not found",
		},
		{
			name:    "group_id — missing recipient records skipped",
			payload: api.GetEmailStatusRequest{GroupID: "grp-b"},
			setup: func(store *mocks.TrackingStore) {
				seedRecipient(t, store, "exists", "grp-b")
				seedGroupIndex(t, store, "grp-b", []string{"exists", "gone"})
			},
			wantRecords: &[]api.EmailRecipientRecord{
				{EmailID: "exists", GroupID: "grp-b", To: "user@example.com", Subject: "Hello"},
			},
		},
		{
			name:    "group_id — unreadable recipient records silently skipped",
			payload: api.GetEmailStatusRequest{GroupID: "grp-c"},
			setup: func(store *mocks.TrackingStore) {
				seedGroupIndex(t, store, "grp-c", []string{"e-bad"})
				store.GetErrFor = map[string]error{"e-bad": errors.New("kv unavailable")}
			},
			// Per-record errors are best-effort skipped; the handler returns an empty list, not an error.
			wantRecords: &[]api.EmailRecipientRecord{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := mocks.NewTrackingStore()
			if tc.setup != nil {
				tc.setup(store)
			}

			handler := service.NewGetEmailStatusHandler(store)

			var data []byte
			switch v := tc.payload.(type) {
			case string:
				data = []byte(v)
			default:
				var err error
				data, err = json.Marshal(v)
				require.NoError(t, err)
			}

			var responded []byte
			respondCount := 0
			handler.HandleData(context.Background(), data, func(d []byte) error {
				respondCount++
				responded = d
				return nil
			})

			assert.Equal(t, 1, respondCount, "respond must be called exactly once")

			if tc.wantErrMsg != "" {
				var errResp api.SendEmailErrorResponse
				require.NoError(t, json.Unmarshal(responded, &errResp))
				assert.Equal(t, tc.wantErrMsg, errResp.Error)
				return
			}

			if tc.wantRecord != nil {
				var got api.EmailRecipientRecord
				require.NoError(t, json.Unmarshal(responded, &got))
				assert.Equal(t, tc.wantRecord.EmailID, got.EmailID)
				assert.Equal(t, tc.wantRecord.GroupID, got.GroupID)
				assert.Equal(t, tc.wantRecord.To, got.To)
				assert.Equal(t, tc.wantRecord.Subject, got.Subject)
			}

			if tc.wantRecords != nil {
				var got []api.EmailRecipientRecord
				require.NoError(t, json.Unmarshal(responded, &got))
				require.Len(t, got, len(*tc.wantRecords))
				for i, want := range *tc.wantRecords {
					assert.Equal(t, want.EmailID, got[i].EmailID)
					assert.Equal(t, want.GroupID, got[i].GroupID)
					assert.Equal(t, want.To, got[i].To)
					assert.Equal(t, want.Subject, got[i].Subject)
				}
			}
		})
	}
}

// Compile-time assertions: both concrete types satisfy domain.TrackingStore.
var _ domain.TrackingStore = domain.NullTrackingStore{}
var _ domain.TrackingStore = (*mocks.TrackingStore)(nil)
