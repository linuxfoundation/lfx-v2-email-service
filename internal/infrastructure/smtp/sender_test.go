// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package smtp

import (
	"context"
	"net"
	"net/textproto"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

var testUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// startCapturingSMTPServer runs a minimal SMTP server that accepts one message
// and sends its DATA payload on the returned channel.
func startCapturingSMTPServer(t *testing.T) (int, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	data := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		tp := textproto.NewConn(conn)
		_ = tp.PrintfLine("220 test ready")
		for {
			line, err := tp.ReadLine()
			if err != nil {
				return
			}
			switch strings.ToUpper(strings.SplitN(line, " ", 2)[0]) {
			case "EHLO", "HELO", "MAIL", "RCPT":
				_ = tp.PrintfLine("250 ok")
			case "DATA":
				_ = tp.PrintfLine("354 go ahead")
				b, err := tp.ReadDotBytes()
				if err != nil {
					return
				}
				data <- string(b)
				_ = tp.PrintfLine("250 queued")
			default:
				_ = tp.PrintfLine("221 bye")
				return
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, data
}

// TestSMTPSender_GroupHandleAndTrackingHeader guards the disclosure this
// service must not make: the group handle is the credential for a group's
// tracking data, so it is returned to the caller but never written into the
// outbound message, and X-LFX-TRACKING-ID carries only the email_id.
func TestSMTPSender_GroupHandleAndTrackingHeader(t *testing.T) {
	t.Parallel()

	supplied := domain.NewGroupHandle()
	tests := []struct {
		name    string
		groupID string
	}{
		{"new group issues a handle", ""},
		{"supplied handle is preserved", supplied},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			port, data := startCapturingSMTPServer(t)
			s := NewSMTPSender(Config{Host: "127.0.0.1", Port: port, From: "noreply@example.org"})

			emailID, groupID, err := s.Send(context.Background(), api.SendEmailRequest{
				To: "bob@example.com", Subject: "Hi", HTML: "<p>Hi</p>", Text: "Hi", GroupID: tc.groupID,
			})
			require.NoError(t, err)

			assert.Regexp(t, testUUIDRe, emailID)
			assert.True(t, domain.IsGroupHandle(groupID), "returned group_id %q must be a service-issued handle", groupID)
			if tc.groupID != "" {
				assert.Equal(t, tc.groupID, groupID)
			}

			var msg string
			select {
			case msg = <-data:
			case <-time.After(5 * time.Second):
				t.Fatal("SMTP server did not receive a message")
			}
			assert.Contains(t, msg, "X-LFX-TRACKING-ID: "+emailID+"\n")
			assert.NotContains(t, msg, groupID, "group handle must not appear in the outbound message")
		})
	}
}
