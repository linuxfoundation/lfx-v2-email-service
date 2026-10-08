// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"regexp"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
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

// isValidGroupID returns true when s is a service-issued group handle
// (domain.IsGroupHandle). The group handle is the credential that grants access
// to a group's tracking records, so a caller-supplied group_id is accepted only
// in the exact format the service issues; caller-chosen labels (including those
// stored as group keys before handles were introduced) are rejected. The format
// is a strict subset of the NATS KV key character set, bounded in length, with
// no '.' characters, so it is also always a valid KV key.
func isValidGroupID(s string) bool {
	return domain.IsGroupHandle(s)
}
