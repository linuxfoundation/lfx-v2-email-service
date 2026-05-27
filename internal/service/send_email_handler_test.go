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
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service/mocks"
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

func TestSendEmailHandler_KVTracking(t *testing.T) {
	t.Parallel()

	t.Run("writes recipient record on successful send", func(t *testing.T) {
		t.Parallel()

		recipientsKV := mocks.NewKeyValue()
		groupIndexKV := mocks.NewKeyValue()
		sender := &mockSender{emailID: "email-1", groupID: "group-1"}
		handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV)

		req := api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", GroupID: "group-1"}
		data, err := json.Marshal(req)
		require.NoError(t, err)

		handler.HandleData(context.Background(), data, func([]byte) error { return nil })

		// Recipient record must be keyed by emailID.
		entry, err := recipientsKV.Get("email-1")
		require.NoError(t, err, "recipient record should be stored under emailID")

		var record api.EmailRecipientRecord
		require.NoError(t, json.Unmarshal(entry.Value(), &record))
		assert.Equal(t, "email-1", record.EmailID)
		assert.Equal(t, "group-1", record.GroupID)
		assert.Equal(t, "alice@example.com", record.To)
		assert.Equal(t, "Hello", record.Subject)
		assert.False(t, record.SentAt.IsZero())
	})

	t.Run("appends emailID to group index", func(t *testing.T) {
		t.Parallel()

		recipientsKV := mocks.NewKeyValue()
		groupIndexKV := mocks.NewKeyValue()
		sender := &mockSender{emailID: "email-2", groupID: "group-2"}
		handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV)

		req := api.SendEmailRequest{To: "bob@example.com", Subject: "Hi", HTML: "<p>Hi</p>", Text: "Hi", GroupID: "group-2"}
		data, _ := json.Marshal(req)
		handler.HandleData(context.Background(), data, func([]byte) error { return nil })

		entry, err := groupIndexKV.Get("group-2")
		require.NoError(t, err, "group index should be written")

		var ids []string
		require.NoError(t, json.Unmarshal(entry.Value(), &ids))
		assert.Equal(t, []string{"email-2"}, ids)
	})

	t.Run("second send appends to existing group index", func(t *testing.T) {
		t.Parallel()

		recipientsKV := mocks.NewKeyValue()
		groupIndexKV := mocks.NewKeyValue()

		for i, id := range []string{"email-a", "email-b"} {
			_ = i
			sender := &mockSender{emailID: id, groupID: "group-3"}
			handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV)
			req := api.SendEmailRequest{To: "c@example.com", Subject: "Hi", HTML: "<p>Hi</p>", Text: "Hi", GroupID: "group-3"}
			data, _ := json.Marshal(req)
			handler.HandleData(context.Background(), data, func([]byte) error { return nil })
		}

		entry, err := groupIndexKV.Get("group-3")
		require.NoError(t, err)

		var ids []string
		require.NoError(t, json.Unmarshal(entry.Value(), &ids))
		assert.ElementsMatch(t, []string{"email-a", "email-b"}, ids)
	})

	t.Run("no KV write when sender returns empty emailID", func(t *testing.T) {
		t.Parallel()

		recipientsKV := mocks.NewKeyValue()
		groupIndexKV := mocks.NewKeyValue()
		sender := &mockSender{emailID: "", groupID: ""}
		handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV)

		req := api.SendEmailRequest{To: "d@example.com", Subject: "Hi", HTML: "<p>Hi</p>", Text: "Hi"}
		data, _ := json.Marshal(req)
		handler.HandleData(context.Background(), data, func([]byte) error { return nil })

		_, err := recipientsKV.Get("")
		assert.Error(t, err, "no record should be written when emailID is empty")
	})

	t.Run("KV write skipped when sender errors", func(t *testing.T) {
		t.Parallel()

		recipientsKV := mocks.NewKeyValue()
		groupIndexKV := mocks.NewKeyValue()
		sender := &mockSender{emailID: "email-x", groupID: "group-x", err: errors.New("smtp down")}
		handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV)

		req := api.SendEmailRequest{To: "e@example.com", Subject: "Hi", HTML: "<p>Hi</p>", Text: "Hi"}
		data, _ := json.Marshal(req)
		handler.HandleData(context.Background(), data, func([]byte) error { return nil })

		_, err := recipientsKV.Get("email-x")
		assert.Error(t, err, "no record should be written on send error")
	})
}
