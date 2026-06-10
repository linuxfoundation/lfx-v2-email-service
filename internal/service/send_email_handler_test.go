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
		name                string
		payload             any
		senderErr           error
		emailID             string
		groupID             string
		wantSent            bool
		wantErrResp         bool
		wantEmailID         string
		wantGroupID         string
		wantFrom            string // assert sender received this From value
		wantFromDisplayName string // assert sender received this FromDisplayName value
		wantReplyTo         string // assert sender received this ReplyTo value
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
		{
			name:                "custom from on allowed domain",
			payload:             api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", From: "events@lfx.linuxfoundation.org"},
			emailID:             "email-uuid-3",
			groupID:             "group-uuid-3",
			wantSent:            true,
			wantEmailID:         "email-uuid-3",
			wantGroupID:         "group-uuid-3",
			wantFrom:            "events@lfx.linuxfoundation.org",
			wantFromDisplayName: "",
		},
		{
			name:        "custom from on disallowed domain",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", From: "attacker@evil.com"},
			wantSent:    false,
			wantErrResp: true,
		},
		{
			name:        "malformed from address",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", From: "not-an-email"},
			wantSent:    false,
			wantErrResp: true,
		},
		{
			name:                "custom from_display_name passed through to sender",
			payload:             api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", From: "events@lfx.linuxfoundation.org", FromDisplayName: "LFX Events"},
			emailID:             "email-uuid-4",
			groupID:             "group-uuid-4",
			wantSent:            true,
			wantEmailID:         "email-uuid-4",
			wantGroupID:         "group-uuid-4",
			wantFrom:            "events@lfx.linuxfoundation.org",
			wantFromDisplayName: "LFX Events",
		},
		{
			// subdomain of linuxfoundation.org is permitted
			name:        "valid reply_to on allowed subdomain",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", ReplyTo: "support@lfx.linuxfoundation.org"},
			emailID:     "email-uuid-6",
			groupID:     "group-uuid-6",
			wantSent:    true,
			wantEmailID: "email-uuid-6",
			wantGroupID: "group-uuid-6",
			wantReplyTo: "support@lfx.linuxfoundation.org",
		},
		{
			// exact base domain is also permitted
			name:        "valid reply_to on base domain",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", ReplyTo: "noreply@linuxfoundation.org"},
			emailID:     "email-uuid-7",
			groupID:     "group-uuid-7",
			wantSent:    true,
			wantEmailID: "email-uuid-7",
			wantGroupID: "group-uuid-7",
			wantReplyTo: "noreply@linuxfoundation.org",
		},
		{
			name:        "reply_to on disallowed domain",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", ReplyTo: "attacker@gmail.com"},
			wantSent:    false,
			wantErrResp: true,
		},
		{
			name:        "malformed reply_to address",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi", ReplyTo: "not-an-email"},
			wantSent:    false,
			wantErrResp: true,
		},
		{
			// The handler passes From/FromDisplayName through unchanged; defaults
			// are resolved later in the SMTP sender, not here.
			name:        "from omitted — sender called with empty from field",
			payload:     api.SendEmailRequest{To: "alice@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi"},
			emailID:     "email-uuid-5",
			groupID:     "group-uuid-5",
			wantSent:    true,
			wantEmailID: "email-uuid-5",
			wantGroupID: "group-uuid-5",
			wantFrom:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sender := &mockSender{err: tc.senderErr, emailID: tc.emailID, groupID: tc.groupID}
			handler := service.NewSendEmailHandler(sender, nil, nil, []string{"lfx.linuxfoundation.org"}, []string{"linuxfoundation.org"}, nil)

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
			if tc.wantFrom != "" || tc.wantSent {
				assert.Equal(t, tc.wantFrom, sender.req.From, "sender received wrong From")
				assert.Equal(t, tc.wantFromDisplayName, sender.req.FromDisplayName, "sender received wrong FromDisplayName")
			}
			if tc.wantReplyTo != "" {
				assert.Equal(t, tc.wantReplyTo, sender.req.ReplyTo, "sender received wrong ReplyTo")
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
		handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV, []string{"lfx.linuxfoundation.org"}, []string{"linuxfoundation.org"}, nil)

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
		handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV, []string{"lfx.linuxfoundation.org"}, []string{"linuxfoundation.org"}, nil)

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
			handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV, []string{"lfx.linuxfoundation.org"}, []string{"linuxfoundation.org"}, nil)
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
		handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV, []string{"lfx.linuxfoundation.org"}, []string{"linuxfoundation.org"}, nil)

		req := api.SendEmailRequest{To: "d@example.com", Subject: "Hi", HTML: "<p>Hi</p>", Text: "Hi"}
		data, _ := json.Marshal(req)
		handler.HandleData(context.Background(), data, func([]byte) error { return nil })

		_, err := recipientsKV.Get("")
		assert.Error(t, err, "no record should be written when emailID is empty")
	})

	t.Run("no group index write when group_id is empty", func(t *testing.T) {
		t.Parallel()

		recipientsKV := mocks.NewKeyValue()
		groupIndexKV := mocks.NewKeyValue()
		sender := &mockSender{emailID: "email-nogroupid", groupID: ""}
		handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV, []string{"lfx.linuxfoundation.org"}, []string{"linuxfoundation.org"}, nil)

		req := api.SendEmailRequest{To: "f@example.com", Subject: "Hi", HTML: "<p>Hi</p>", Text: "Hi"}
		data, _ := json.Marshal(req)
		handler.HandleData(context.Background(), data, func([]byte) error { return nil })

		_, err := recipientsKV.Get("email-nogroupid")
		assert.NoError(t, err, "recipient record should still be written")

		_, err = groupIndexKV.Get("")
		assert.Error(t, err, "group index should not be written under empty key")
	})

	t.Run("KV write skipped when sender errors", func(t *testing.T) {
		t.Parallel()

		recipientsKV := mocks.NewKeyValue()
		groupIndexKV := mocks.NewKeyValue()
		sender := &mockSender{emailID: "email-x", groupID: "group-x", err: errors.New("smtp down")}
		handler := service.NewSendEmailHandler(sender, recipientsKV, groupIndexKV, []string{"lfx.linuxfoundation.org"}, []string{"linuxfoundation.org"}, nil)

		req := api.SendEmailRequest{To: "e@example.com", Subject: "Hi", HTML: "<p>Hi</p>", Text: "Hi"}
		data, _ := json.Marshal(req)
		handler.HandleData(context.Background(), data, func([]byte) error { return nil })

		_, err := recipientsKV.Get("email-x")
		assert.Error(t, err, "no record should be written on send error")
	})
}

func TestSendEmailHandler_RecipientDomainAllowlist(t *testing.T) {
	t.Parallel()

	baseReq := api.SendEmailRequest{To: "user@example.com", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi"}
	validReqLFX := api.SendEmailRequest{To: "user@linuxfoundation.org", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi"}
	subdomainReq := api.SendEmailRequest{To: "user@sub.linuxfoundation.org", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi"}
	malformedToReq := api.SendEmailRequest{To: "not-an-email", Subject: "Hello", HTML: "<p>Hi</p>", Text: "Hi"}

	makeHandler := func(recipientDomains []string) (*service.SendEmailHandler, *mockSender) {
		s := &mockSender{emailID: "email-r", groupID: "group-r"}
		h := service.NewSendEmailHandler(s, nil, nil, nil, nil, recipientDomains)
		return h, s
	}

	respond := func(responded *[]byte) func([]byte) error {
		return func(d []byte) error {
			*responded = d
			return nil
		}
	}

	t.Run("empty allowlist permits any recipient", func(t *testing.T) {
		t.Parallel()
		h, s := makeHandler(nil)
		var resp []byte
		data, _ := json.Marshal(baseReq)
		h.HandleData(context.Background(), data, respond(&resp))
		assert.True(t, s.called, "sender should be called when allowlist is empty")
		var r api.SendEmailResponse
		require.NoError(t, json.Unmarshal(resp, &r))
		assert.Equal(t, "email-r", r.EmailID)
	})

	t.Run("exact domain match is permitted", func(t *testing.T) {
		t.Parallel()
		h, s := makeHandler([]string{"linuxfoundation.org"})
		var resp []byte
		data, _ := json.Marshal(validReqLFX)
		h.HandleData(context.Background(), data, respond(&resp))
		assert.True(t, s.called, "sender should be called for exact domain match")
		var r api.SendEmailResponse
		require.NoError(t, json.Unmarshal(resp, &r))
		assert.Equal(t, "email-r", r.EmailID)
	})

	t.Run("subdomain of allowed base domain is permitted", func(t *testing.T) {
		t.Parallel()
		h, s := makeHandler([]string{"linuxfoundation.org"})
		var resp []byte
		data, _ := json.Marshal(subdomainReq)
		h.HandleData(context.Background(), data, respond(&resp))
		assert.True(t, s.called, "sender should be called for subdomain of allowed base domain")
		var r api.SendEmailResponse
		require.NoError(t, json.Unmarshal(resp, &r))
		assert.Equal(t, "email-r", r.EmailID)
	})

	t.Run("non-matching domain is skipped — empty success response, no send", func(t *testing.T) {
		t.Parallel()
		h, s := makeHandler([]string{"linuxfoundation.org"})
		var resp []byte
		data, _ := json.Marshal(baseReq) // to: user@example.com
		h.HandleData(context.Background(), data, respond(&resp))
		assert.False(t, s.called, "sender must not be called for blocked recipient domain")
		// Response must be a valid (empty) SendEmailResponse, not an error.
		var r api.SendEmailResponse
		require.NoError(t, json.Unmarshal(resp, &r), "response should be a valid SendEmailResponse")
		assert.Empty(t, r.EmailID, "email_id should be empty for blocked recipient")
		var errR api.SendEmailErrorResponse
		_ = json.Unmarshal(resp, &errR)
		assert.Empty(t, errR.Error, "response must not be an error reply")
	})

	t.Run("case-insensitive domain matching", func(t *testing.T) {
		t.Parallel()
		h, s := makeHandler([]string{"LinuxFoundation.ORG"})
		var resp []byte
		data, _ := json.Marshal(validReqLFX) // to: user@linuxfoundation.org (lower)
		h.HandleData(context.Background(), data, respond(&resp))
		assert.True(t, s.called, "domain matching must be case-insensitive")
	})

	t.Run("malformed to address with active allowlist is skipped", func(t *testing.T) {
		t.Parallel()
		h, s := makeHandler([]string{"linuxfoundation.org"})
		var resp []byte
		data, _ := json.Marshal(malformedToReq)
		h.HandleData(context.Background(), data, respond(&resp))
		assert.False(t, s.called, "sender must not be called for unparseable recipient address")
		var r api.SendEmailResponse
		require.NoError(t, json.Unmarshal(resp, &r))
		assert.Empty(t, r.EmailID)
	})

	t.Run("multiple allowed domains — match on second entry", func(t *testing.T) {
		t.Parallel()
		h, s := makeHandler([]string{"example.org", "linuxfoundation.org"})
		var resp []byte
		data, _ := json.Marshal(validReqLFX) // linuxfoundation.org is second entry
		h.HandleData(context.Background(), data, respond(&resp))
		assert.True(t, s.called, "sender should be called when domain matches any entry in the allowlist")
	})
}
