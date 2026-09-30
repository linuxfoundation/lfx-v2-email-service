// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import "regexp"

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
// and contains only characters from the NATS KV key character set.
// group_id is caller-supplied (not required to be a UUID), so the bound is
// length + character set rather than UUID format.
func isValidGroupID(s string) bool {
	return len(s) <= maxGroupIDLen && groupIDRe.MatchString(s)
}
