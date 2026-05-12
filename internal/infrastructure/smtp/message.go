// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package smtp

import (
	"crypto/rand"
	"fmt"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

func generateBoundary() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("===============%x==", b)
}

func generateMessageID(from string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	domain := "localhost"
	if addr, err := mail.ParseAddress(from); err == nil && strings.Contains(addr.Address, "@") {
		domain = strings.Split(addr.Address, "@")[1]
	}
	return fmt.Sprintf("<%x.%d@%s>", b, time.Now().UnixNano(), domain)
}

// buildEmailMessage constructs a multipart/alternative MIME message (HTML + plain text).
func buildEmailMessage(to, subject, htmlContent, textContent, from string) string {
	messageID := generateMessageID(from)
	boundary := generateBoundary()
	var b strings.Builder

	b.WriteString(fmt.Sprintf("From: LFX One <%s>\r\n", from))
	b.WriteString(fmt.Sprintf("To: %s\r\n", to))
	b.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	b.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	b.WriteString(fmt.Sprintf("Message-ID: %s\r\n", messageID))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary))
	b.WriteString("\r\n")

	b.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(textContent)
	b.WriteString("\r\n")

	b.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlContent)
	b.WriteString("\r\n")

	b.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	return b.String()
}

// sendMessage delivers a pre-built MIME message via SMTP.
func sendMessage(to, message string, cfg Config) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	var auth smtp.Auth
	if cfg.Username != "" && cfg.Password != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}

	fromAddr, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return fmt.Errorf("invalid From address: %w", err)
	}
	toAddr, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("invalid recipient address: %w", err)
	}

	return smtp.SendMail(addr, auth, fromAddr.Address, []string{toAddr.Address}, []byte(message))
}
