// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import "regexp"

// uuidRe matches the canonical 8-4-4-4-12 UUID format (case-insensitive).
// email_id and group_id are always service-generated UUIDs; enforcing this
// format before any KV call ensures caller-supplied values cannot embed
// oversized strings that would exceed the NATS server's max_control_line
// limit and close the service's shared NATS connection.
var uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func isValidUUID(s string) bool {
	return uuidRe.MatchString(s)
}
