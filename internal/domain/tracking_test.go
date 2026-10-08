// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stretchr/testify/assert"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
)

func TestNewGroupHandle(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)
	for range 1000 {
		h := domain.NewGroupHandle()
		assert.True(t, domain.IsGroupHandle(h), "issued handle %q must validate", h)
		assert.False(t, seen[h], "issued handles must be unique")
		seen[h] = true
	}
}

func TestIsGroupHandle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"valid", "grp_0123456789abcdef0123456789abcdef", true},
		{"empty", "", false},
		{"uuid", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", false},
		{"caller label", "invite-batch-abc123", false},
		{"uppercase hex", "grp_0123456789ABCDEF0123456789ABCDEF", false},
		{"short", "grp_0123", false},
		{"suffix", "grp_0123456789abcdef0123456789abcdef.x", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, domain.IsGroupHandle(tc.input))
		})
	}
}

func TestNullTrackingStore_GroupHandles(t *testing.T) {
	t.Parallel()
	ok, err := domain.NullTrackingStore{}.GroupExists(t.Context(), domain.NewGroupHandle())
	require.NoError(t, err)
	assert.True(t, ok)

	err = domain.NullTrackingStore{}.AppendToGroup(t.Context(), domain.NewGroupHandle(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	assert.ErrorIs(t, err, domain.ErrTrackingUnavailable)
}
