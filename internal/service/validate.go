// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"regexp"
	"strings"
)

// uuidRe matches the canonical 8-4-4-4-12 UUID format (case-insensitive).
// email_id is always a service-generated UUID; enforcing this format before
// any KV call ensures the value cannot embed an oversized string that would
// exceed the NATS server's max_control_line limit and close the service's
// shared NATS connection.
var uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func isValidUUID(s string) bool {
	return uuidRe.MatchString(s)
}

// maxGroupIDLen is the maximum number of bytes a caller-supplied group_id may
// contain. The value must fit as a NATS KV key without causing the service's
// own NATS connection to exceed the server's max_control_line limit (default
// 4096 bytes). 256 bytes leaves ample room for the KV subject prefix while
// still supporting any practical correlation-ID format.
const maxGroupIDLen = 256

// groupIDRe matches the NATS KV key character set: letters, digits, and
// the punctuation that nats.go's keyValid regex permits.
var groupIDRe = regexp.MustCompile(`^[-/_=.a-zA-Z0-9]+$`)

// isValidGroupID returns true when s is non-empty, at most maxGroupIDLen bytes,
// contains only characters from the NATS KV key character set, does not start
// or end with '.', and contains no consecutive dots. The leading/trailing '.'
// check mirrors nats.go v1.47.0 keyValid. The consecutive-dot check goes beyond
// keyValid: nats.go accepts "a..b" but the resulting NATS subject contains an
// empty token that nats-server v2.14.6 will not route, causing Put/Get to fail
// with "no responders available". group_id is caller-supplied (not required to
// be a UUID), so the bound is length + character set rather than UUID format.
func isValidGroupID(s string) bool {
	if len(s) == 0 || len(s) > maxGroupIDLen {
		return false
	}
	if s[0] == '.' || s[len(s)-1] == '.' {
		return false
	}
	if strings.Contains(s, "..") {
		return false
	}
	return groupIDRe.MatchString(s)
}
