// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mocks_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service/mocks"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// TestTrackingStore_UpdateRecord_FailedUpdateLeavesRecordUnchanged verifies
// that, like kv.Store, an update rejected as unserialisable does not leak
// in-place edits to slices or pointed-to times into the stored record.
func TestTrackingStore_UpdateRecord_FailedUpdateLeavesRecordUnchanged(t *testing.T) {
	t.Parallel()

	const emailID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	failedAt := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	store := mocks.NewTrackingStore()
	store.PutRecord(emailID, api.EmailRecipientRecord{
		EmailID:       emailID,
		ClickEventIDs: []string{"a"},
		ClickList:     []api.ClickEvent{{EventID: "a", Link: "https://example.com/"}},
		OpenedAtList:  []api.OpenEvent{{EventID: "o"}},
		FailedAt:      &failedAt,
	})
	before, ok := store.GetStoredRecord(emailID)
	require.True(t, ok)
	want := *before.FailedAt

	err := store.UpdateRecord(context.Background(), emailID, func(r *api.EmailRecipientRecord) {
		r.ClickEventIDs[0] = "b"
		r.ClickList[0].Link = "https://changed.example.com/"
		r.OpenedAtList[0].EventID = "changed"
		*r.FailedAt = failedAt.Add(time.Hour)
		bad := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		r.LastOpenedAt = &bad
	})
	require.ErrorIs(t, err, domain.ErrRecordUnencodable)

	after, ok := store.GetStoredRecord(emailID)
	require.True(t, ok)
	assert.Equal(t, []string{"a"}, after.ClickEventIDs)
	assert.Equal(t, "https://example.com/", after.ClickList[0].Link)
	assert.Equal(t, "o", after.OpenedAtList[0].EventID)
	require.NotNil(t, after.FailedAt)
	assert.Equal(t, want, *after.FailedAt)
	assert.Nil(t, after.LastOpenedAt)
}
