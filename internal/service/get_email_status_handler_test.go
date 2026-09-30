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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service/mocks"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

const (
	statusEmailUUID1   = "11111111-1111-1111-1111-111111111111"
	statusEmailUUID2   = "22222222-2222-2222-2222-222222222222"
	statusEmailUUIDErr = "77777777-7777-7777-7777-777777777777"
	statusGroupUUIDA   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	statusGroupUUIDB   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	statusGroupUUIDF   = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	statusEmailMissing = "99999999-9999-9999-9999-999999999999"
	statusGroupMissing = "88888888-8888-8888-8888-888888888888"
	statusEmailExists  = "33333333-3333-3333-3333-333333333333"
	statusEmailGone    = "44444444-4444-4444-4444-444444444444"
	statusEmailBad     = "55555555-5555-5555-5555-555555555555"
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
			payload:    api.GetEmailStatusRequest{EmailID: statusEmailUUID1, GroupID: statusGroupUUIDA},
			wantErrMsg: "only one of email_id or group_id may be set",
		},
		{
			name:       "email_id not a UUID",
			payload:    api.GetEmailStatusRequest{EmailID: "email-1"},
			wantErrMsg: "invalid email_id",
		},
		{
			name:       "email_id 100 KiB oversized value",
			payload:    api.GetEmailStatusRequest{EmailID: strings.Repeat("a", 100_000)},
			wantErrMsg: "invalid email_id",
		},
		{
			name:       "group_id not a UUID",
			payload:    api.GetEmailStatusRequest{GroupID: "grp-a"},
			wantErrMsg: "invalid group_id",
		},
		{
			name:       "group_id 100 KiB oversized value",
			payload:    api.GetEmailStatusRequest{GroupID: strings.Repeat("a", 100_000)},
			wantErrMsg: "invalid group_id",
		},
		{
			name:    "email_id happy path",
			payload: api.GetEmailStatusRequest{EmailID: statusEmailUUID1},
			setup: func(store *mocks.TrackingStore) {
				seedRecipient(t, store, statusEmailUUID1, statusGroupUUIDA)
			},
			wantRecord: &api.EmailRecipientRecord{EmailID: statusEmailUUID1, GroupID: statusGroupUUIDA, To: "user@example.com", Subject: "Hello"},
		},
		{
			name:       "email_id not found",
			payload:    api.GetEmailStatusRequest{EmailID: statusEmailMissing},
			wantErrMsg: "not found",
		},
		{
			name:    "email_id KV internal error",
			payload: api.GetEmailStatusRequest{EmailID: statusEmailUUIDErr},
			setup: func(store *mocks.TrackingStore) {
				store.GetErrFor = map[string]error{statusEmailUUIDErr: errors.New("kv unavailable")}
			},
			wantErrMsg: "internal error",
		},
		{
			name:    "group_id happy path",
			payload: api.GetEmailStatusRequest{GroupID: statusGroupUUIDA},
			setup: func(store *mocks.TrackingStore) {
				seedRecipient(t, store, statusEmailUUID1, statusGroupUUIDA)
				seedRecipient(t, store, statusEmailUUID2, statusGroupUUIDA)
				seedGroupIndex(t, store, statusGroupUUIDA, []string{statusEmailUUID1, statusEmailUUID2})
			},
			wantRecords: &[]api.EmailRecipientRecord{
				{EmailID: statusEmailUUID1, GroupID: statusGroupUUIDA, To: "user@example.com", Subject: "Hello"},
				{EmailID: statusEmailUUID2, GroupID: statusGroupUUIDA, To: "user@example.com", Subject: "Hello"},
			},
		},
		{
			name:       "group_id not found",
			payload:    api.GetEmailStatusRequest{GroupID: statusGroupMissing},
			wantErrMsg: "not found",
		},
		{
			name:    "group_id — missing recipient records skipped",
			payload: api.GetEmailStatusRequest{GroupID: statusGroupUUIDB},
			setup: func(store *mocks.TrackingStore) {
				seedRecipient(t, store, statusEmailExists, statusGroupUUIDB)
				seedGroupIndex(t, store, statusGroupUUIDB, []string{statusEmailExists, statusEmailGone})
			},
			wantRecords: &[]api.EmailRecipientRecord{
				{EmailID: statusEmailExists, GroupID: statusGroupUUIDB, To: "user@example.com", Subject: "Hello"},
			},
		},
		{
			name:    "group_id — unreadable recipient records silently skipped",
			payload: api.GetEmailStatusRequest{GroupID: statusGroupUUIDF},
			setup: func(store *mocks.TrackingStore) {
				seedGroupIndex(t, store, statusGroupUUIDF, []string{statusEmailBad})
				store.GetErrFor = map[string]error{statusEmailBad: errors.New("kv unavailable")}
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
