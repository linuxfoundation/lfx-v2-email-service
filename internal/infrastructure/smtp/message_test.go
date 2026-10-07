// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package smtp

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildEmailMessage_Headers(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage(
		"bob@example.com",
		"Test Subject",
		"<p>Hello Bob</p>",
		"Hello Bob",
		"noreply@lfx.linuxfoundation.org",
		"LFX Self Serve",
		"",
		"",
		"",
	)

	assert.Contains(t, msg, `From: "LFX Self Serve" <noreply@lfx.linuxfoundation.org>`)
	assert.Contains(t, msg, "To: bob@example.com")
	assert.Contains(t, msg, "Subject: Test Subject")
	assert.Contains(t, msg, "MIME-Version: 1.0")
	assert.Contains(t, msg, "Content-Type: multipart/alternative;")
	assert.Contains(t, msg, "Message-ID:")
	assert.Contains(t, msg, "Date:")
}

func TestBuildEmailMessage_ConfigurationSetHeader(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "LFX Self Serve", "", "my-config-set", "")
	assert.Contains(t, msg, "X-SES-CONFIGURATION-SET: my-config-set")

	msgNoSet := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "LFX Self Serve", "", "", "")
	assert.NotContains(t, msgNoSet, "X-SES-CONFIGURATION-SET")
}

func TestBuildEmailMessage_TrackingIDHeader(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "LFX Self Serve", "", "", "group-uuid/email-uuid")
	assert.Contains(t, msg, "X-LFX-TRACKING-ID: group-uuid/email-uuid")

	msgNoTracking := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "LFX Self Serve", "", "", "")
	assert.NotContains(t, msgNoTracking, "X-LFX-TRACKING-ID")
}

func TestBuildEmailMessage_BothParts(t *testing.T) {
	t.Parallel()

	htmlBody := "<p>Hello Bob</p>"
	textBody := "Hello Bob"

	msg := buildEmailMessage("bob@example.com", "Subject", htmlBody, textBody, "from@example.com", "LFX Self Serve", "", "", "")

	assert.Contains(t, msg, "Content-Type: text/plain; charset=UTF-8")
	assert.Contains(t, msg, "Content-Type: text/html; charset=UTF-8")
	assert.Contains(t, msg, htmlBody)
	assert.Contains(t, msg, textBody)
}

func TestBuildEmailMessage_BoundaryPresent(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage("to@example.com", "Sub", "<b>x</b>", "x", "from@example.com", "LFX Self Serve", "", "", "")

	contentTypeLine := ""
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(line, "Content-Type: multipart/alternative;") {
			contentTypeLine = line
			break
		}
	}
	require.NotEmpty(t, contentTypeLine, "multipart Content-Type header not found")

	idx := strings.Index(contentTypeLine, `boundary="`)
	require.NotEqual(t, -1, idx)
	rest := contentTypeLine[idx+len(`boundary="`):]
	endIdx := strings.Index(rest, `"`)
	require.NotEqual(t, -1, endIdx)
	boundary := rest[:endIdx]
	require.NotEmpty(t, boundary)

	assert.Contains(t, msg, "--"+boundary+"\r\n", "part separator not found")
	assert.Contains(t, msg, "--"+boundary+"--\r\n", "closing boundary not found")
}

func TestBuildEmailMessage_CustomFromDisplayName(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage(
		"bob@example.com",
		"Test Subject",
		"<p>Hi</p>",
		"Hi",
		"events@lfx.linuxfoundation.org",
		"LFX Events",
		"",
		"",
		"",
	)

	assert.Contains(t, msg, "events@lfx.linuxfoundation.org")
	assert.Contains(t, msg, "LFX Events")
	assert.NotContains(t, msg, "LFX Self Serve")
}

func TestBuildEmailMessage_DefaultFromDisplayName(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage(
		"bob@example.com",
		"Test Subject",
		"<p>Hi</p>",
		"Hi",
		"noreply@lfx.linuxfoundation.org",
		"LFX Self Serve",
		"",
		"",
		"",
	)

	assert.Contains(t, msg, `"LFX Self Serve" <noreply@lfx.linuxfoundation.org>`)
}

func TestBuildEmailMessage_FromDisplayName_InjectionStripped(t *testing.T) {
	t.Parallel()

	// CR/LF in display name must not result in injected headers.
	// sanitizeHeaderValue strips CR/LF before Q-encoding, so the literal
	// "\r\nBcc:" sequence must not appear in the output.
	msg := buildEmailMessage(
		"bob@example.com",
		"Test Subject",
		"<p>Hi</p>",
		"Hi",
		"noreply@lfx.linuxfoundation.org",
		"Evil\r\nBcc: attacker@evil.com",
		"",
		"",
		"",
	)

	// The injected header prefix must not appear as a literal CRLF sequence.
	assert.NotContains(t, msg, "\r\nBcc:")
}

func TestBuildEmailMessage_ReplyToHeader(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "LFX Self Serve", "support@lfx.linuxfoundation.org", "", "")
	assert.Contains(t, msg, "Reply-To: <support@lfx.linuxfoundation.org>")
}

func TestBuildEmailMessage_ReplyToQuotedLocalPartStaysSingleMailbox(t *testing.T) {
	t.Parallel()

	for _, replyTo := range []string{
		`"attacker@evil.com, LF"@lfx.linuxfoundation.org`,
		`"Support <attacker@evil.com>, x"@lfx.linuxfoundation.org`,
		`"a@evil.com"@sub.linuxfoundation.org`,
		`"alice"@lfx.linuxfoundation.org`,
	} {
		msg := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "LFX Self Serve", replyTo, "", "")

		var header string
		for _, line := range strings.Split(msg, "\r\n") {
			if v, ok := strings.CutPrefix(line, "Reply-To: "); ok {
				header = v
				break
			}
		}
		require.NotEmpty(t, header, "Reply-To header missing for %q", replyTo)

		list, err := mail.ParseAddressList(header)
		require.NoError(t, err, "Reply-To header %q must parse", header)
		require.Len(t, list, 1, "Reply-To header %q must be a single mailbox", header)
		at := strings.LastIndex(list[0].Address, "@")
		require.Positive(t, at)
		want, err := mail.ParseAddress(replyTo)
		require.NoError(t, err)
		assert.Equal(t, want.Address, list[0].Address, "Reply-To mailbox must round-trip unchanged")
		assert.Equal(t, want.Address[strings.LastIndex(want.Address, "@")+1:], list[0].Address[at+1:])
	}
}

func TestBuildEmailMessage_ReplyToRedundantQuotesNormalised(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "LFX Self Serve", `"alice"@lfx.linuxfoundation.org`, "", "")
	assert.Contains(t, msg, "Reply-To: <alice@lfx.linuxfoundation.org>\r\n")
}

func TestBuildEmailMessage_ReplyToOmittedWhenEmpty(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "LFX Self Serve", "", "", "")
	assert.NotContains(t, msg, "Reply-To:")
}

func TestGenerateMessageID_ContainsDomain(t *testing.T) {
	t.Parallel()

	id := generateMessageID("noreply@lfx.linuxfoundation.org")
	assert.Contains(t, id, "lfx.linuxfoundation.org")
	assert.True(t, strings.HasPrefix(id, "<"), "message-id should start with <")
	assert.True(t, strings.HasSuffix(id, ">"), "message-id should end with >")
}

func TestGenerateMessageID_FallbackDomain(t *testing.T) {
	t.Parallel()

	id := generateMessageID("not-an-email")
	assert.Contains(t, id, "localhost")
}

func TestSendMessage_MalformedAddressDoesNotEchoInput(t *testing.T) {
	t.Parallel()

	cfg := Config{Host: "127.0.0.1", Port: 1}
	tests := []struct {
		name    string
		to      string
		from    string
		wantErr error
		secret  string
	}{
		{"comma-separated recipients", "a@x.com, b@y.com", "noreply@example.org", errInvalidRecipientAddress, "b@y.com"},
		{"space-separated recipients", "a@x.com b@y.com", "noreply@example.org", errInvalidRecipientAddress, "b@y.com"},
		{"trailing text", "a@x.com trailing-secret", "noreply@example.org", errInvalidRecipientAddress, "trailing-secret"},
		{"invalid utf-8", "\"a\xffsecret\"@x.com", "noreply@example.org", errInvalidRecipientAddress, "secret"},
		{"malformed from", "a@x.com", "noreply@example.org, c@z.com", errInvalidFromAddress, "c@z.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := sendMessage(context.Background(), tt.to, tt.from, "msg", cfg)
			require.ErrorIs(t, err, tt.wantErr)
			assert.NotContains(t, err.Error(), tt.secret)
		})
	}
}

func TestRedactAddressInError(t *testing.T) {
	t.Parallel()

	assert.NoError(t, redactAddressInError(nil, "jane@example.com"))

	orig := errors.New("421 service not available")
	assert.Same(t, orig, redactAddressInError(orig, "jane@example.com"), "error without the address is returned unchanged")

	err := redactAddressInError(errors.New("554 Message rejected: Email address is not verified: <JANE@Example.com>"), "jane@example.com")
	assert.NotContains(t, strings.ToLower(err.Error()), "jane@example.com")
	assert.Contains(t, err.Error(), "<j****@example.com>")
}

func TestGenerateBoundary_Unique(t *testing.T) {
	t.Parallel()

	b1 := generateBoundary()
	b2 := generateBoundary()
	assert.NotEqual(t, b1, b2, "boundaries should be unique")
}
