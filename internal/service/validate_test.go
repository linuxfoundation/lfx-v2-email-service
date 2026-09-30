// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
