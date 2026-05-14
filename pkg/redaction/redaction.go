// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package redaction provides utilities for redacting sensitive information in logs.
package redaction

import "strings"

// Redact redacts sensitive strings, showing only the first few characters.
func Redact(sensitive string) string {
	if len(sensitive) == 0 {
		return ""
	}
	runes := []rune(sensitive)
	n := len(runes)
	if n <= 2 {
		return "**"
	}
	if n <= 5 {
		return string(runes[0]) + "****"
	}
	return string(runes[:3]) + "****"
}

// RedactEmail redacts an email address, keeping the domain visible for debugging.
func RedactEmail(email string) string {
	if email == "" {
		return ""
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return Redact(email)
	}
	return Redact(parts[0]) + "@" + parts[1]
}
