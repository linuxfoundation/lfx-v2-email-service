// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/service"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

type mockSender struct {
	called  bool
	req     api.SendEmailRequest
	emailID string
	groupID string
	err     error
}

func (m *mockSender) Send(_ context.Context, req api.SendEmailRequest) (string, string, error) {
	m.called = true
	m.req = req
	return m.emailID, m.groupID, m.err
}

func TestSendEmailHandler_HandleData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		payload     any
		senderErr   error
		emailID     string
		groupID     string
		wantSent    bool
		wantErrResp bool
		wantEmailID string
		wantGroupID string
	}{
		{
			name:        "happy path — ids returned",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi"},
			emailID:     "email-uuid-1",
			groupID:     "group-uuid-1",
			wantSent:    true,
			wantEmailID: "email-uuid-1",
			wantGroupID: "group-uuid-1",
		},
		{
			name:        "happy path — caller provides group_id",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", GroupID: "caller-group"},
			emailID:     "email-uuid-2",
			groupID:     "caller-group",
			wantSent:    true,
			wantEmailID: "email-uuid-2",
			wantGroupID: "caller-group",
		},
		{
			name:        "sender error",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi"},
			senderErr:   errors.New("smtp down"),
			wantSent:    true,
			wantErrResp: true,
		},
		{
			name:        "missing html and text",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello"},
			wantSent:    false,
			wantErrResp: true,
		},
		{
			name:        "missing to",
			payload:     api.SendEmailRequest{Subject: "Hello"},
			wantSent:    false,
			wantErrResp: true,
		},
		{
			name:        "missing subject",
			payload:     api.SendEmailRequest{To: "alice@example.com"},
			wantSent:    false,
			wantErrResp: true,
		},
		{
			name:        "malformed JSON",
			payload:     "{not json",
			wantSent:    false,
			wantErrResp: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sender := &mockSender{err: tc.senderErr, emailID: tc.emailID, groupID: tc.groupID}
			handler := service.NewSendEmailHandler(sender, nil, nil)

			var data []byte
			switch v := tc.payload.(type) {
			case string:
				data = []byte(v)
			default:
				var err error
				data, err = json.Marshal(tc.payload)
				require.NoError(t, err)
			}

			var responded []byte
			respondCount := 0
			respond := func(d []byte) error {
				respondCount++
				responded = d
				return nil
			}

			handler.HandleData(context.Background(), data, respond)

			assert.Equal(t, 1, respondCount, "respond must be called exactly once")
			assert.Equal(t, tc.wantSent, sender.called, "sender.called")

			if tc.wantEmailID != "" {
				var resp api.SendEmailResponse
				require.NoError(t, json.Unmarshal(responded, &resp))
				assert.Equal(t, tc.wantEmailID, resp.EmailID)
				assert.Equal(t, tc.wantGroupID, resp.GroupID)
			}
			if tc.wantErrResp {
				require.NotNil(t, responded)
				var errResp api.SendEmailErrorResponse
				require.NoError(t, json.Unmarshal(responded, &errResp))
				assert.NotEmpty(t, errResp.Error)
			}
		})
	}
}
