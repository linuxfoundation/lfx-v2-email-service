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

	"github.com/linuxfoundation/lfx-v2-email-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service/mocks"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

func seedRecipient(t *testing.T, kv *mocks.KeyValue, emailID, groupID string) api.EmailRecipientRecord {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	record := api.EmailRecipientRecord{
		EmailID: emailID,
		GroupID: groupID,
		To:      "user@example.com",
		Subject: "Hello",
		SentAt:  now,
	}
	b, err := json.Marshal(record)
	require.NoError(t, err)
	_, err = kv.Put(emailID, b)
	require.NoError(t, err)
	return record
}

func seedGroupIndex(t *testing.T, kv *mocks.KeyValue, groupID string, emailIDs []string) {
	t.Helper()
	b, err := json.Marshal(emailIDs)
	require.NoError(t, err)
	_, err = kv.Put(groupID, b)
	require.NoError(t, err)
}

func TestGetEmailStatusHandler_HandleData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		payload     any
		setup       func(recipients, groupIndex *mocks.KeyValue)
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
			setup: func(recipients, _ *mocks.KeyValue) {
				seedRecipient(t, recipients, "email-1", "group-1")
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
			setup: func(recipients, _ *mocks.KeyValue) {
				recipients.GetErrFor = map[string]error{"bad-key": errors.New("kv unavailable")}
			},
			wantErrMsg: "internal error",
		},
		{
			name:    "group_id happy path",
			payload: api.GetEmailStatusRequest{GroupID: "grp-a"},
			setup: func(recipients, groupIndex *mocks.KeyValue) {
				seedRecipient(t, recipients, "e1", "grp-a")
				seedRecipient(t, recipients, "e2", "grp-a")
				seedGroupIndex(t, groupIndex, "grp-a", []string{"e1", "e2"})
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
			setup: func(recipients, groupIndex *mocks.KeyValue) {
				seedRecipient(t, recipients, "exists", "grp-b")
				seedGroupIndex(t, groupIndex, "grp-b", []string{"exists", "gone"})
			},
			wantRecords: &[]api.EmailRecipientRecord{
				{EmailID: "exists", GroupID: "grp-b", To: "user@example.com", Subject: "Hello"},
			},
		},
		{
			name:    "group_id — recipient KV internal error fails request",
			payload: api.GetEmailStatusRequest{GroupID: "grp-c"},
			setup: func(recipients, groupIndex *mocks.KeyValue) {
				seedGroupIndex(t, groupIndex, "grp-c", []string{"e-bad"})
				recipients.GetErrFor = map[string]error{"e-bad": errors.New("kv unavailable")}
			},
			wantErrMsg: "internal error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recipients := mocks.NewKeyValue()
			groupIndex := mocks.NewKeyValue()
			if tc.setup != nil {
				tc.setup(recipients, groupIndex)
			}

			handler := service.NewGetEmailStatusHandler(recipients, groupIndex)

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
