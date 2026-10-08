// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service/mocks"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

func TestGetEmailEngagementAnalyticsHandler_HandleData(t *testing.T) {
	t.Parallel()

	const (
		analyticsGroupUUID  = "grp_11111111111111111111111111111111"
		analyticsEmailUUID1 = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		analyticsEmailUUID2 = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	)

	tests := []struct {
		name       string
		payload    any
		setup      func(store *mocks.TrackingStore)
		wantErrMsg string
		wantResp   *api.GetEmailEngagementAnalyticsResponse
	}{
		{
			name:       "malformed JSON",
			payload:    "{not json",
			wantErrMsg: "invalid request payload",
		},
		{
			name:       "group_id missing",
			payload:    api.GetEmailEngagementAnalyticsRequest{},
			wantErrMsg: "group_id is required",
		},
		{
			name:       "group_id over 256 chars",
			payload:    api.GetEmailEngagementAnalyticsRequest{GroupID: strings.Repeat("a", 257)},
			wantErrMsg: "invalid group_id",
		},
		{
			name:       "group_id invalid char (space)",
			payload:    api.GetEmailEngagementAnalyticsRequest{GroupID: "has space"},
			wantErrMsg: "invalid group_id",
		},
		{
			name:       "group_id 100 KiB oversized value",
			payload:    api.GetEmailEngagementAnalyticsRequest{GroupID: strings.Repeat("a", 100_000)},
			wantErrMsg: "invalid group_id",
		},
		{
			name:    "group_id caller-chosen legacy label — refused even though group exists",
			payload: api.GetEmailEngagementAnalyticsRequest{GroupID: "invite-batch-abc123"},
			setup: func(store *mocks.TrackingStore) {
				store.PutGroup("invite-batch-abc123", []string{analyticsEmailUUID1})
			},
			wantErrMsg: "invalid group_id",
		},
		{
			name:       "group_id not found",
			payload:    api.GetEmailEngagementAnalyticsRequest{GroupID: analyticsGroupUUID},
			wantErrMsg: "not found",
		},
		{
			name:    "happy path — counts aggregated correctly",
			payload: api.GetEmailEngagementAnalyticsRequest{GroupID: analyticsGroupUUID},
			setup: func(store *mocks.TrackingStore) {
				store.PutRecord(analyticsEmailUUID1, api.EmailRecipientRecord{
					EmailID:   analyticsEmailUUID1,
					GroupID:   analyticsGroupUUID,
					Delivered: true,
					Opened:    true,
					OpenCount: 2,
				})
				store.PutRecord(analyticsEmailUUID2, api.EmailRecipientRecord{
					EmailID: analyticsEmailUUID2,
					GroupID: analyticsGroupUUID,
					Failed:  true,
				})
				store.PutGroup(analyticsGroupUUID, []string{analyticsEmailUUID1, analyticsEmailUUID2})
			},
			wantResp: &api.GetEmailEngagementAnalyticsResponse{
				GroupID:      analyticsGroupUUID,
				TotalSent:    2,
				Delivered:    1,
				Opened:       2,
				UniqueOpened: 1,
				Failed:       1,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := mocks.NewTrackingStore()
			if tc.setup != nil {
				tc.setup(store)
			}

			handler := service.NewGetEmailEngagementAnalyticsHandler(store)

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

			if tc.wantResp != nil {
				var got api.GetEmailEngagementAnalyticsResponse
				require.NoError(t, json.Unmarshal(responded, &got))
				assert.Equal(t, *tc.wantResp, got)
			}
		})
	}
}
