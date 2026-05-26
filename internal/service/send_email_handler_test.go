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
	called    bool
	req       api.SendEmailRequest
	messageID string
	err       error
}

func (m *mockSender) Send(_ context.Context, req api.SendEmailRequest) (string, error) {
	m.called = true
	m.req = req
	return m.messageID, m.err
}

func TestSendEmailHandler_HandleData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		payload     any
		senderErr   error
		wantSent    bool
		wantErrResp bool
		wantNilResp bool
	}{
		{
			name:        "happy path",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi"},
			wantSent:    true,
			wantNilResp: true,
		},
		{
			name:        "happy path with correlation id",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", CorrelationID: "corr-123", SourceService: "invite-service"},
			wantSent:    true,
			wantNilResp: true,
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

			sender := &mockSender{err: tc.senderErr}
			// nil kvStore — no KV writes in unit tests
			handler := service.NewSendEmailHandler(sender, nil)

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
			respondedNil := false
			respondCount := 0
			respond := func(d []byte) error {
				respondCount++
				if d == nil {
					respondedNil = true
				} else {
					responded = d
				}
				return nil
			}

			handler.HandleData(context.Background(), data, respond)

			assert.Equal(t, 1, respondCount, "respond must be called exactly once")
			assert.Equal(t, tc.wantSent, sender.called, "sender.called")

			if tc.wantNilResp {
				assert.True(t, respondedNil, "expected nil (success) response")
				assert.Nil(t, responded)
			}
			if tc.wantErrResp {
				require.NotNil(t, responded, "expected error response body")
				var errResp api.SendEmailErrorResponse
				require.NoError(t, json.Unmarshal(responded, &errResp))
				assert.NotEmpty(t, errResp.Error)
			}
		})
	}
}
