// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractEmailID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers []sesHeader
		want    string
	}{
		{
			name: "tracking id found",
			headers: []sesHeader{
				{Name: "From", Value: "sender@example.com"},
				{Name: "X-LFX-TRACKING-ID", Value: "group-uuid/email-uuid"},
			},
			want: "email-uuid",
		},
		{
			name: "case-insensitive match",
			headers: []sesHeader{
				{Name: "x-lfx-tracking-id", Value: "g/e"},
			},
			want: "e",
		},
		{
			name: "no slash — returns full value",
			headers: []sesHeader{
				{Name: "X-LFX-TRACKING-ID", Value: "nogroup"},
			},
			want: "nogroup",
		},
		{
			name:    "header absent",
			headers: []sesHeader{{Name: "From", Value: "x@example.com"}},
			want:    "",
		},
		{
			name:    "no headers",
			headers: nil,
			want:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, extractEmailID(tc.headers))
		})
	}
}
