// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

func TestExtractMessageID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers []sesHeader
		want    string
	}{
		{
			name: "message-id found",
			headers: []sesHeader{
				{Name: "From", Value: "sender@example.com"},
				{Name: "Message-ID", Value: "<abc123.456@example.com>"},
			},
			want: "abc123.456@example.com",
		},
		{
			name: "case-insensitive match",
			headers: []sesHeader{
				{Name: "message-id", Value: "<lower@example.com>"},
			},
			want: "lower@example.com",
		},
		{
			name:    "no headers",
			headers: nil,
			want:    "",
		},
		{
			name: "no message-id header",
			headers: []sesHeader{
				{Name: "From", Value: "sender@example.com"},
			},
			want: "",
		},
		{
			name: "message-id without angle brackets",
			headers: []sesHeader{
				{Name: "Message-ID", Value: "naked@example.com"},
			},
			want: "naked@example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractMessageID(tc.headers)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestApplyOpen(t *testing.T) {
	t.Parallel()

	h := &EngagementEventHandler{}
	record := &api.EmailTrackingRecord{OpenCount: 0}

	h.applyOpen(t.Context(), record, &sesEvent{})
	require.Equal(t, 1, record.OpenCount)
	require.NotNil(t, record.FirstOpenedAt)
	require.NotNil(t, record.LastOpenedAt)
	first := record.FirstOpenedAt

	h.applyOpen(t.Context(), record, &sesEvent{})
	assert.Equal(t, 2, record.OpenCount)
	assert.Equal(t, first, record.FirstOpenedAt, "first_opened_at should not change on second open")
	assert.NotNil(t, record.LastOpenedAt)
}

func TestApplyBounce_IdempotentAfterFirst(t *testing.T) {
	t.Parallel()

	h := &EngagementEventHandler{}
	record := &api.EmailTrackingRecord{}

	h.applyBounce(record, &sesEvent{})
	require.NotNil(t, record.BouncedAt)
	first := record.BouncedAt

	h.applyBounce(record, &sesEvent{})
	assert.Equal(t, first, record.BouncedAt, "bounced_at should not change after first bounce")
}
