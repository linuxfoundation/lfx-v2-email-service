// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package smtp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"net/smtp"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/linuxfoundation/lfx-v2-email-service/pkg/redaction"
)

// Sentinel errors returned by sendMessage when an address cannot be parsed.
// They deliberately carry no input text so callers can log them safely.
var (
	errInvalidFromAddress      = errors.New("invalid From address")
	errInvalidRecipientAddress = errors.New("invalid recipient address")
)

func generateBoundary() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return fmt.Sprintf("===============%x==", b)
}

func generateMessageID(from string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	domain := "localhost"
	if addr, err := mail.ParseAddress(from); err == nil && strings.Contains(addr.Address, "@") {
		domain = strings.Split(addr.Address, "@")[1]
	}
	return fmt.Sprintf("<%x.%d@%s>", b, time.Now().UnixNano(), domain)
}

// sanitizeHeaderValue strips CR and LF characters to prevent SMTP header injection.
func sanitizeHeaderValue(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	return v
}

// buildEmailMessage constructs a multipart/alternative MIME message (HTML + plain text).
// configurationSet, when non-empty, adds an X-SES-CONFIGURATION-SET header so SES routes
// engagement events to the named configuration set.
// trackingID, when non-empty, adds an X-LFX-TRACKING-ID header in the form group_id/email_id
// so the SQS poller can correlate SES events back to the KV record.
// fromDisplayName is the display name shown in the From header (e.g. "LFX Self Serve").
// replyTo, when non-empty, sets the Reply-To header to direct mail-client replies to
// a different address than From.
func buildEmailMessage(to, subject, htmlContent, textContent, from, fromDisplayName, replyTo, configurationSet, trackingID string) string {
	messageID := generateMessageID(from)
	boundary := generateBoundary()
	var b strings.Builder

	// Extract the bare address so a cfg.From value like "Name <addr>" doesn't
	// produce an invalid "From: Display Name <Name <addr>>" header.
	fromAddr := from
	if parsed, err := mail.ParseAddress(from); err == nil {
		fromAddr = parsed.Address
	}
	// Use mail.Address.String() to produce a properly RFC 5322-encoded From header.
	// This quotes display names that contain commas or other special characters,
	// preventing them from being mis-parsed as a mailbox list.
	fmt.Fprintf(&b, "From: %s\r\n", (&mail.Address{Name: sanitizeHeaderValue(fromDisplayName), Address: sanitizeHeaderValue(fromAddr)}).String())
	fmt.Fprintf(&b, "To: %s\r\n", sanitizeHeaderValue(to))
	if replyTo != "" {
		replyToAddr := replyTo
		if parsed, err := mail.ParseAddress(replyTo); err == nil {
			replyToAddr = parsed.Address
		}
		// Serialise through mail.Address.String() (as for From) so a local part that
		// needs quoting is re-quoted and the header is always a single mailbox.
		fmt.Fprintf(&b, "Reply-To: %s\r\n", (&mail.Address{Address: sanitizeHeaderValue(replyToAddr)}).String())
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", sanitizeHeaderValue(subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: %s\r\n", messageID)
	if configurationSet != "" {
		fmt.Fprintf(&b, "X-SES-CONFIGURATION-SET: %s\r\n", sanitizeHeaderValue(configurationSet))
	}
	if trackingID != "" {
		fmt.Fprintf(&b, "X-LFX-TRACKING-ID: %s\r\n", sanitizeHeaderValue(trackingID))
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary)
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(textContent)
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlContent)
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.String()
}

// sendMessage delivers a pre-built MIME message via SMTP.
// from is the resolved envelope sender address (MAIL FROM); it must match a
// verified SES domain. It runs the blocking smtp.SendMail call in a goroutine
// so ctx cancellation (including a caller-supplied deadline) is respected.
func sendMessage(ctx context.Context, to, from, message string, cfg Config) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	var auth smtp.Auth
	if cfg.Username != "" && cfg.Password != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}

	// mail.ParseAddress errors can quote the unparsed input (e.g. a second
	// recipient after a comma), so return sentinels instead of wrapping them.
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return errInvalidFromAddress
	}
	toAddr, err := mail.ParseAddress(to)
	if err != nil {
		return errInvalidRecipientAddress
	}

	type result struct{ err error }
	ch := make(chan result, 1)
	go func() {
		ch <- result{smtp.SendMail(addr, auth, fromAddr.Address, []string{toAddr.Address}, []byte(message))}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-ch:
		return redactAddressesInError(r.err, toAddr.Address, fromAddr.Address)
	}
}

// redactAddressesInError replaces case-insensitive occurrences of each addr in
// err's text with its redacted form, so SMTP server replies that echo an
// envelope address (e.g. "554 Message rejected: ... <addr>") do not leak it
// into logs. err is returned unchanged when it contains none of addrs. The
// redacted error deliberately does not wrap err, so the unredacted text stays
// unreachable.
//
// All addresses are matched in a single pass, longest first, so an address
// that is a substring of another (e.g. a@x.com inside ba@x.com) cannot break
// up the longer one before it is redacted.
func redactAddressesInError(err error, addrs ...string) error {
	if err == nil {
		return err
	}
	byLower := make(map[string]string, len(addrs))
	var patterns []string
	for _, addr := range addrs {
		if addr == "" {
			continue
		}
		if _, dup := byLower[strings.ToLower(addr)]; dup {
			continue
		}
		byLower[strings.ToLower(addr)] = addr
		patterns = append(patterns, regexp.QuoteMeta(addr))
	}
	if len(patterns) == 0 {
		return err
	}
	// Go regexp alternation is leftmost-first, so list longer addresses first.
	sort.Slice(patterns, func(i, j int) bool { return len(patterns[i]) > len(patterns[j]) })
	re, reErr := regexp.Compile("(?i)(?:" + strings.Join(patterns, "|") + ")")
	if reErr != nil {
		return errors.New("smtp server rejected message")
	}
	msg := err.Error()
	if !re.MatchString(msg) {
		return err
	}
	return errors.New(re.ReplaceAllStringFunc(msg, func(m string) string {
		if addr, ok := byLower[strings.ToLower(m)]; ok {
			return redaction.RedactEmail(addr)
		}
		return redaction.Redact(m)
	}))
}
