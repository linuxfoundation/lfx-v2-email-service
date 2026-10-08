// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
)

func TestIsValidUUID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"lowercase uuid", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", true},
		{"uppercase uuid", "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA", true},
		{"mixed case uuid", "Aaaaaaaa-Aaaa-Aaaa-Aaaa-Aaaaaaaaaaaa", true},
		{"numeric uuid", "00000000-0000-0000-0000-000000000000", true},
		{"hex digits uuid", "f47ac10b-58cc-4372-a567-0e02b2c3d479", true},
		{"empty string", "", false},
		{"short string", "email-1", false},
		{"missing hyphens", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
		{"too long", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaaa", false},
		{"too short", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa", false},
		{"invalid chars", "gggggggg-gggg-gggg-gggg-gggggggggggg", false},
		{"spaces", "aaaaaaaa aaaa aaaa aaaa aaaaaaaaaaaa", false},
		{"100 KiB key-legal chars", strings.Repeat("a", 100_000), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isValidUUID(tc.input))
		})
	}
}

func TestIsValidGroupID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"service-issued handle", "grp_0123456789abcdef0123456789abcdef", true},
		{"freshly issued handle", domain.NewGroupHandle(), true},
		{"empty string", "", false},
		{"uuid format — legacy service-generated group_id", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", false},
		{"slug style — legacy caller-chosen label", "invite-batch-abc123", false},
		{"uppercase hex", "grp_0123456789ABCDEF0123456789ABCDEF", false},
		{"too short", "grp_0123456789abcdef0123456789abcde", false},
		{"too long", "grp_0123456789abcdef0123456789abcdef0", false},
		{"missing prefix", "0123456789abcdef0123456789abcdef", false},
		{"wrong prefix", "grq_0123456789abcdef0123456789abcdef", false},
		{"non-hex char", "grp_0123456789abcdef0123456789abcdeg", false},
		{"trailing newline", "grp_0123456789abcdef0123456789abcdef\n", false},
		{"slash suffix", "grp_0123456789abcdef0123456789abcdef/x", false},
		{"100 KiB oversized value", strings.Repeat("a", 100_000), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isValidGroupID(tc.input))
		})
	}
}
