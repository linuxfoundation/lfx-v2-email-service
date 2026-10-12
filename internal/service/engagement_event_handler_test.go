// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"
	"time"

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

// TestParseTimestamp_OutOfJSONRange verifies that a timestamp whose UTC instant
// falls outside the years time.Time.MarshalJSON accepts (0..9999) is replaced
// by the processing time, while in-range values, including the extremes, are kept.
func TestParseTimestamp_OutOfJSONRange(t *testing.T) {
	t.Parallel()

	for _, s := range []string{
		"9999-12-31T23:00:00-05:00", // UTC year 10000
		"0000-01-01T00:00:00+01:00", // UTC year -1
	} {
		before := time.Now().UTC()
		got := parseTimestamp(s)
		after := time.Now().UTC()
		assert.False(t, got.Before(before) || got.After(after), "%s: want time.Now() fallback, got %s", s, got)
		_, err := got.MarshalJSON()
		assert.NoError(t, err, s)
	}

	for s, want := range map[string]time.Time{
		"9999-12-31T23:59:59Z":      time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		"9999-12-31T18:59:59-05:00": time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		"0000-01-01T00:00:00Z":      time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC),
		"2026-01-02T10:04:05-05:00": time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC),
	} {
		assert.Equal(t, want, parseTimestamp(s), s)
	}
}
