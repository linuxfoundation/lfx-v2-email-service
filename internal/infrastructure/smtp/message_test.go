// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package smtp

import (
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
		"",
		"",
	)

	assert.Contains(t, msg, "From: LFX Self Serve <noreply@lfx.linuxfoundation.org>")
	assert.Contains(t, msg, "To: bob@example.com")
	assert.Contains(t, msg, "Subject: Test Subject")
	assert.Contains(t, msg, "MIME-Version: 1.0")
	assert.Contains(t, msg, "Content-Type: multipart/alternative;")
	assert.Contains(t, msg, "Message-ID:")
	assert.Contains(t, msg, "Date:")
}

func TestBuildEmailMessage_ConfigurationSetHeader(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "my-config-set", "")
	assert.Contains(t, msg, "X-SES-CONFIGURATION-SET: my-config-set")

	msgNoSet := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "", "")
	assert.NotContains(t, msgNoSet, "X-SES-CONFIGURATION-SET")
}

func TestBuildEmailMessage_TrackingIDHeader(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "", "group-uuid/email-uuid")
	assert.Contains(t, msg, "X-LFX-TRACKING-ID: group-uuid/email-uuid")

	msgNoTracking := buildEmailMessage("bob@example.com", "Sub", "<p>Hi</p>", "Hi", "from@example.com", "", "")
	assert.NotContains(t, msgNoTracking, "X-LFX-TRACKING-ID")
}

func TestBuildEmailMessage_BothParts(t *testing.T) {
	t.Parallel()

	htmlBody := "<p>Hello Bob</p>"
	textBody := "Hello Bob"

	msg := buildEmailMessage("bob@example.com", "Subject", htmlBody, textBody, "from@example.com", "", "")

	assert.Contains(t, msg, "Content-Type: text/plain; charset=UTF-8")
	assert.Contains(t, msg, "Content-Type: text/html; charset=UTF-8")
	assert.Contains(t, msg, htmlBody)
	assert.Contains(t, msg, textBody)
}

func TestBuildEmailMessage_BoundaryPresent(t *testing.T) {
	t.Parallel()

	msg := buildEmailMessage("to@example.com", "Sub", "<b>x</b>", "x", "from@example.com", "", "")

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

func TestGenerateBoundary_Unique(t *testing.T) {
	t.Parallel()

	b1 := generateBoundary()
	b2 := generateBoundary()
	assert.NotEqual(t, b1, b2, "boundaries should be unique")
}
