// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package kv_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	kvinfra "github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/kv"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/service/mocks"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

func newStore(t *testing.T) (*kvinfra.Store, *mocks.KeyValue, *mocks.KeyValue) {
	t.Helper()
	recipientsKV := mocks.NewKeyValue()
	groupIndexKV := mocks.NewKeyValue()
	return kvinfra.New(recipientsKV, groupIndexKV), recipientsKV, groupIndexKV
}

func TestStore_WriteRecord(t *testing.T) {
	t.Parallel()
	store, recipientsKV, _ := newStore(t)

	r := api.EmailRecipientRecord{EmailID: "e1", GroupID: "g1", To: "a@b.com", Subject: "Hi", SentAt: time.Now().UTC()}
	require.NoError(t, store.WriteRecord(context.Background(), "e1", r))

	got, err := store.GetRecord(context.Background(), "e1")
	require.NoError(t, err)
	assert.Equal(t, r.EmailID, got.EmailID)
	assert.Equal(t, r.GroupID, got.GroupID)

	// raw KV entry also exists
	_, err = recipientsKV.Get("e1")
	require.NoError(t, err)
}

func TestStore_GetRecord_NotFound(t *testing.T) {
	t.Parallel()
	store, _, _ := newStore(t)
	_, err := store.GetRecord(context.Background(), "missing")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestStore_AppendToGroup(t *testing.T) {
	t.Parallel()

	t.Run("creates new group entry", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)
		require.NoError(t, store.AppendToGroup(context.Background(), "g1", "e1"))
		entry, err := groupIndexKV.Get("g1")
		require.NoError(t, err)
		assert.Contains(t, string(entry.Value()), "e1")
	})

	t.Run("appends to existing entry", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)
		require.NoError(t, store.AppendToGroup(context.Background(), "g2", "e1"))
		require.NoError(t, store.AppendToGroup(context.Background(), "g2", "e2"))

		// Assert the raw group-index value contains both IDs so we verify e2 was
		// actually appended and not silently lost by GetGroupRecords skipping absent records.
		entry, err := groupIndexKV.Get("g2")
		require.NoError(t, err)
		raw := string(entry.Value())
		assert.Contains(t, raw, "e1")
		assert.Contains(t, raw, "e2")
	})

	t.Run("concurrent first-send: Create loses race, retries via Update", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)

		// Pre-create the key as if another goroutine won the race.
		_, err := groupIndexKV.Create("g-race", []byte(`["winner"]`))
		require.NoError(t, err)

		// AppendToGroup should detect ErrKeyExists on Create and fall through to
		// a second attempt using Update.
		require.NoError(t, store.AppendToGroup(context.Background(), "g-race", "e-late"))

		entry, err := groupIndexKV.Get("g-race")
		require.NoError(t, err)
		raw := string(entry.Value())
		assert.Contains(t, raw, "winner")
		assert.Contains(t, raw, "e-late")
	})
}

func TestStore_GetGroupRecords(t *testing.T) {
	t.Parallel()

	t.Run("happy path returns records in index order", func(t *testing.T) {
		t.Parallel()
		store, _, _ := newStore(t)

		r1 := api.EmailRecipientRecord{EmailID: "e1", GroupID: "g1", To: "a@b.com", Subject: "S1", SentAt: time.Now().UTC()}
		r2 := api.EmailRecipientRecord{EmailID: "e2", GroupID: "g1", To: "b@b.com", Subject: "S2", SentAt: time.Now().UTC()}
		require.NoError(t, store.WriteRecord(context.Background(), "e1", r1))
		require.NoError(t, store.WriteRecord(context.Background(), "e2", r2))
		require.NoError(t, store.AppendToGroup(context.Background(), "g1", "e1"))
		require.NoError(t, store.AppendToGroup(context.Background(), "g1", "e2"))

		got, totalIDs, err := store.GetGroupRecords(context.Background(), "g1")
		require.NoError(t, err)
		assert.Equal(t, 2, totalIDs)
		require.Len(t, got, 2)
		assert.Equal(t, "e1", got[0].EmailID)
		assert.Equal(t, "e2", got[1].EmailID)
	})

	t.Run("returns ErrNotFound for unknown group", func(t *testing.T) {
		t.Parallel()
		store, _, _ := newStore(t)
		_, _, err := store.GetGroupRecords(context.Background(), "unknown")
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("silently skips absent individual records, totalIDs reflects index count", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)

		r1 := api.EmailRecipientRecord{EmailID: "e-exists", GroupID: "g2", To: "a@b.com", Subject: "S", SentAt: time.Now().UTC()}
		require.NoError(t, store.WriteRecord(context.Background(), "e-exists", r1))

		// Seed group index manually to include a missing record ID.
		b := []byte(`["e-exists","e-gone"]`)
		_, err := groupIndexKV.Put("g2", b)
		require.NoError(t, err)

		got, totalIDs, err := store.GetGroupRecords(context.Background(), "g2")
		require.NoError(t, err)
		assert.Equal(t, 2, totalIDs, "totalIDs must reflect raw index count, not fetched record count")
		require.Len(t, got, 1)
		assert.Equal(t, "e-exists", got[0].EmailID)
	})
}

func TestStore_UpdateRecord(t *testing.T) {
	t.Parallel()

	t.Run("applies fn and persists result", func(t *testing.T) {
		t.Parallel()
		store, _, _ := newStore(t)

		r := api.EmailRecipientRecord{EmailID: "e1", GroupID: "g1", To: "a@b.com", Subject: "Hi", SentAt: time.Now().UTC()}
		require.NoError(t, store.WriteRecord(context.Background(), "e1", r))

		err := store.UpdateRecord(context.Background(), "e1", func(rec *api.EmailRecipientRecord) {
			rec.Delivered = true
		})
		require.NoError(t, err)

		got, err := store.GetRecord(context.Background(), "e1")
		require.NoError(t, err)
		assert.True(t, got.Delivered)
	})

	t.Run("no-ops silently when record absent", func(t *testing.T) {
		t.Parallel()
		store, _, _ := newStore(t)
		called := false
		err := store.UpdateRecord(context.Background(), "not-there", func(_ *api.EmailRecipientRecord) { called = true })
		require.NoError(t, err)
		assert.False(t, called, "fn must not be called for absent record")
	})

	t.Run("retries on write conflict and succeeds", func(t *testing.T) {
		t.Parallel()
		store, recipientsKV, _ := newStore(t)

		r := api.EmailRecipientRecord{EmailID: "e-conflict", GroupID: "g1", To: "a@b.com", Subject: "Hi", SentAt: time.Now().UTC()}
		require.NoError(t, store.WriteRecord(context.Background(), "e-conflict", r))

		// Force the first Update to fail unconditionally so the retry path is exercised.
		// The second attempt reads the current revision and succeeds normally.
		recipientsKV.UpdateErrOnce = true

		err := store.UpdateRecord(context.Background(), "e-conflict", func(rec *api.EmailRecipientRecord) {
			rec.Delivered = true
		})
		require.NoError(t, err)
		assert.False(t, recipientsKV.UpdateErrOnce, "UpdateErrOnce must have been consumed by the retry")

		got, err := store.GetRecord(context.Background(), "e-conflict")
		require.NoError(t, err)
		assert.True(t, got.Delivered)
	})
}

// Compile-time assertion: *kvinfra.Store satisfies domain.TrackingStore.
var _ domain.TrackingStore = (*kvinfra.Store)(nil)
