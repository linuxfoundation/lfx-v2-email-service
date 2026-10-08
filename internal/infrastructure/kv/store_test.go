// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package kv_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	kvinfra "github.com/linuxfoundation/lfx-v2-email-service/internal/infrastructure/kv"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// ── test double ──────────────────────────────────────────────────────────────
// fakeBucket is an in-memory implementation of the 4-method kvBucket seam.
// It implements exactly the methods Store calls; nothing more.

var errWrongRevision = errors.New("wrong last revision")

type fakeEntry struct {
	key      string
	value    []byte
	revision uint64
}

func (e *fakeEntry) Bucket() string               { return "fake" }
func (e *fakeEntry) Key() string                  { return e.key }
func (e *fakeEntry) Value() []byte                { return append([]byte(nil), e.value...) }
func (e *fakeEntry) Revision() uint64             { return e.revision }
func (e *fakeEntry) Delta() uint64                { return 0 }
func (e *fakeEntry) Created() time.Time           { return time.Time{} }
func (e *fakeEntry) Operation() natsgo.KeyValueOp { return natsgo.KeyValuePut }

type fakeBucket struct {
	mu            sync.Mutex
	entries       map[string]*fakeEntry
	UpdateErrOnce bool     // if true, the next Update call fails and resets to false
	UpdateErr     error    // if non-nil, every Update call returns this error
	calls         []string // keys passed to any bucket method, in call order
}

// calledKeys returns a copy of the keys passed to the bucket so far.
func (b *fakeBucket) calledKeys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.calls...)
}

func newFakeBucket() *fakeBucket {
	return &fakeBucket{entries: make(map[string]*fakeEntry)}
}

func (b *fakeBucket) Get(key string) (natsgo.KeyValueEntry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, key)
	e, ok := b.entries[key]
	if !ok {
		return nil, natsgo.ErrKeyNotFound
	}
	return e, nil
}

func (b *fakeBucket) Put(key string, value []byte) (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, key)
	rev := uint64(1)
	if e, ok := b.entries[key]; ok {
		rev = e.revision + 1
	}
	b.entries[key] = &fakeEntry{key: key, value: append([]byte(nil), value...), revision: rev}
	return rev, nil
}

func (b *fakeBucket) Update(key string, value []byte, last uint64) (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, key)
	if b.UpdateErr != nil {
		return 0, b.UpdateErr
	}
	if b.UpdateErrOnce {
		b.UpdateErrOnce = false
		return 0, errWrongRevision
	}
	e, ok := b.entries[key]
	if !ok || e.revision != last {
		return 0, errWrongRevision
	}
	rev := e.revision + 1
	b.entries[key] = &fakeEntry{key: key, value: append([]byte(nil), value...), revision: rev}
	return rev, nil
}

func (b *fakeBucket) Create(key string, value []byte) (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, key)
	if _, ok := b.entries[key]; ok {
		return 0, natsgo.ErrKeyExists
	}
	b.entries[key] = &fakeEntry{key: key, value: append([]byte(nil), value...), revision: 1}
	return 1, nil
}

// cancelOnGet is a recipients bucket that cancels the scan's context on every
// Get, simulating a deadline that expires while a chunk is being read.
type cancelOnGet struct {
	*fakeBucket
	cancel context.CancelFunc
}

func (b *cancelOnGet) Get(key string) (natsgo.KeyValueEntry, error) {
	b.cancel()
	return b.fakeBucket.Get(key)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// Group-index entries must be UUIDs; ScanGroupRecords skips anything else.
const (
	uuid1 = "11111111-1111-4111-8111-111111111111"
	uuid2 = "22222222-2222-4222-8222-222222222222"
	uuid3 = "33333333-3333-4333-8333-333333333333"
)

// scanAll collects every record ScanGroupRecords yields for groupID.
func scanAll(ctx context.Context, store *kvinfra.Store, groupID string) ([]api.EmailRecipientRecord, int, error) {
	var got []api.EmailRecipientRecord
	total, err := store.ScanGroupRecords(ctx, groupID, 0, api.MaxGroupEmails, func(r api.EmailRecipientRecord) bool {
		got = append(got, r)
		return true
	})
	return got, total, err
}

// seedFullGroup writes a group index holding n distinct UUID email_ids.
func seedFullGroup(t *testing.T, groupIndexKV *fakeBucket, groupID string, n int) []string {
	t.Helper()
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
	}
	b, err := json.Marshal(ids)
	require.NoError(t, err)
	_, err = groupIndexKV.Put(groupID, b)
	require.NoError(t, err)
	return ids
}

func newStore(t *testing.T) (*kvinfra.Store, *fakeBucket, *fakeBucket) {
	t.Helper()
	recipientsKV := newFakeBucket()
	groupIndexKV := newFakeBucket()
	return kvinfra.New(recipientsKV, groupIndexKV), recipientsKV, groupIndexKV
}

// ── tests ─────────────────────────────────────────────────────────────────────

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
		require.NoError(t, store.AppendToGroup(context.Background(), "g1", uuid1))
		entry, err := groupIndexKV.Get("g1")
		require.NoError(t, err)
		assert.Contains(t, string(entry.Value()), uuid1)
	})

	t.Run("appends to existing entry", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)
		require.NoError(t, store.AppendToGroup(context.Background(), "g2", uuid1))
		require.NoError(t, store.AppendToGroup(context.Background(), "g2", uuid2))

		// Assert the raw group-index value contains both IDs so we verify e2 was
		// actually appended and not silently lost by ScanGroupRecords skipping absent records.
		entry, err := groupIndexKV.Get("g2")
		require.NoError(t, err)
		raw := string(entry.Value())
		assert.Contains(t, raw, uuid1)
		assert.Contains(t, raw, uuid2)
	})

	t.Run("concurrent first-send: Create loses race, retries via Update", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)

		// Pre-create the key as if another goroutine won the race.
		_, err := groupIndexKV.Create("g-race", []byte(`["`+uuid1+`"]`))
		require.NoError(t, err)

		// AppendToGroup should detect ErrKeyExists on Create and fall through to
		// a second attempt using Update.
		require.NoError(t, store.AppendToGroup(context.Background(), "g-race", uuid2))

		entry, err := groupIndexKV.Get("g-race")
		require.NoError(t, err)
		raw := string(entry.Value())
		assert.Contains(t, raw, uuid1)
		assert.Contains(t, raw, uuid2)
	})
}

func TestStore_GroupExists(t *testing.T) {
	t.Parallel()
	store, _, _ := newStore(t)
	ctx := context.Background()

	ok, err := store.GroupExists(ctx, "g1")
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, store.AppendToGroup(ctx, "g1", uuid1))
	ok, err = store.GroupExists(ctx, "g1")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestStore_ScanGroupRecords(t *testing.T) {
	t.Parallel()

	t.Run("happy path returns records in index order", func(t *testing.T) {
		t.Parallel()
		store, _, _ := newStore(t)

		r1 := api.EmailRecipientRecord{EmailID: uuid1, GroupID: "g1", To: "a@b.com", Subject: "S1", SentAt: time.Now().UTC()}
		r2 := api.EmailRecipientRecord{EmailID: uuid2, GroupID: "g1", To: "b@b.com", Subject: "S2", SentAt: time.Now().UTC()}
		require.NoError(t, store.WriteRecord(context.Background(), uuid1, r1))
		require.NoError(t, store.WriteRecord(context.Background(), uuid2, r2))
		require.NoError(t, store.AppendToGroup(context.Background(), "g1", uuid1))
		require.NoError(t, store.AppendToGroup(context.Background(), "g1", uuid2))

		got, totalIDs, err := scanAll(context.Background(), store, "g1")
		require.NoError(t, err)
		assert.Equal(t, 2, totalIDs)
		require.Len(t, got, 2)
		assert.Equal(t, uuid1, got[0].EmailID)
		assert.Equal(t, uuid2, got[1].EmailID)
	})

	t.Run("skips index entries whose record belongs to another group", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)

		own := api.EmailRecipientRecord{EmailID: uuid1, GroupID: "g3", To: "a@b.com", Subject: "S", SentAt: time.Now().UTC()}
		foreign := api.EmailRecipientRecord{EmailID: uuid2, GroupID: "other", To: "x@y.com", Subject: "Secret", SentAt: time.Now().UTC()}
		require.NoError(t, store.WriteRecord(context.Background(), uuid1, own))
		require.NoError(t, store.WriteRecord(context.Background(), uuid2, foreign))

		// Index altered to list another group's email.
		_, err := groupIndexKV.Put("g3", []byte(`["`+uuid1+`","`+uuid2+`"]`))
		require.NoError(t, err)

		got, totalIDs, err := scanAll(context.Background(), store, "g3")
		require.NoError(t, err)
		assert.Equal(t, 2, totalIDs)
		require.Len(t, got, 1)
		assert.Equal(t, uuid1, got[0].EmailID)
	})

	t.Run("returns ErrNotFound for unknown group", func(t *testing.T) {
		t.Parallel()
		store, _, _ := newStore(t)
		_, _, err := scanAll(context.Background(), store, "unknown")
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("silently skips absent individual records, totalIDs reflects index count", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)

		r1 := api.EmailRecipientRecord{EmailID: uuid1, GroupID: "g2", To: "a@b.com", Subject: "S", SentAt: time.Now().UTC()}
		require.NoError(t, store.WriteRecord(context.Background(), uuid1, r1))

		// Seed group index manually to include a missing record ID.
		b := []byte(`["` + uuid1 + `","` + uuid2 + `"]`)
		_, err := groupIndexKV.Put("g2", b)
		require.NoError(t, err)

		got, totalIDs, err := scanAll(context.Background(), store, "g2")
		require.NoError(t, err)
		assert.Equal(t, 2, totalIDs, "totalIDs must reflect raw index count, not fetched record count")
		require.Len(t, got, 1)
		assert.Equal(t, uuid1, got[0].EmailID)
	})

	t.Run("skips non-UUID index entries without touching the bucket", func(t *testing.T) {
		t.Parallel()
		store, recipientsKV, groupIndexKV := newStore(t)

		r1 := api.EmailRecipientRecord{EmailID: uuid1, GroupID: "g3", To: "a@b.com", Subject: "S1", SentAt: time.Now().UTC()}
		r3 := api.EmailRecipientRecord{EmailID: uuid3, GroupID: "g3", To: "c@b.com", Subject: "S3", SentAt: time.Now().UTC()}
		require.NoError(t, store.WriteRecord(context.Background(), uuid1, r1))
		require.NoError(t, store.WriteRecord(context.Background(), uuid3, r3))

		// A poisoned index: an oversized KV-legal id and a short non-UUID id
		// sit between two valid entries.
		oversized := strings.Repeat("a", 5000)
		b := []byte(`["` + uuid1 + `","` + oversized + `","not-a-uuid","` + uuid3 + `"]`)
		_, err := groupIndexKV.Put("g3", b)
		require.NoError(t, err)

		got, totalIDs, err := scanAll(context.Background(), store, "g3")
		require.NoError(t, err)
		assert.Equal(t, 4, totalIDs, "totalIDs must reflect raw index count")
		require.Len(t, got, 2)
		assert.Equal(t, uuid1, got[0].EmailID)
		assert.Equal(t, uuid3, got[1].EmailID)

		for _, k := range recipientsKV.calledKeys() {
			assert.NotEqual(t, oversized, k, "oversized id must never reach the bucket")
			assert.NotEqual(t, "not-a-uuid", k, "non-UUID id must never reach the bucket")
		}
	})
}

func TestStore_GroupCap(t *testing.T) {
	t.Parallel()

	t.Run("AppendToGroup rejects a full group without writing", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)
		seedFullGroup(t, groupIndexKV, "gfull", api.MaxGroupEmails)
		before, err := groupIndexKV.Get("gfull")
		require.NoError(t, err)

		err = store.AppendToGroup(context.Background(), "gfull", uuid1)
		require.ErrorIs(t, err, domain.ErrGroupFull)

		after, err := groupIndexKV.Get("gfull")
		require.NoError(t, err)
		assert.Equal(t, before.Revision(), after.Revision(), "full group index must not be rewritten")
		assert.Less(t, len(after.Value()), 1<<20/2, "a full group index stays far below the 1 MiB maxValueSize")
	})

	t.Run("AppendToGroup accepts the last free slot", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)
		seedFullGroup(t, groupIndexKV, "galmost", api.MaxGroupEmails-1)
		require.NoError(t, store.AppendToGroup(context.Background(), "galmost", uuid1))
		require.ErrorIs(t, store.AppendToGroup(context.Background(), "galmost", uuid2), domain.ErrGroupFull)
	})

	t.Run("GroupExists reports a full group", func(t *testing.T) {
		t.Parallel()
		store, _, groupIndexKV := newStore(t)
		seedFullGroup(t, groupIndexKV, "gfull", api.MaxGroupEmails)
		ok, err := store.GroupExists(context.Background(), "gfull")
		assert.True(t, ok)
		assert.ErrorIs(t, err, domain.ErrGroupFull)
	})
}

func TestStore_ScanGroupRecords_Bounds(t *testing.T) {
	t.Parallel()

	seed := func(t *testing.T, n int) (*kvinfra.Store, *fakeBucket, []string) {
		t.Helper()
		store, recipientsKV, groupIndexKV := newStore(t)
		ids := seedFullGroup(t, groupIndexKV, "g", n)
		for _, id := range ids {
			require.NoError(t, store.WriteRecord(context.Background(), id, api.EmailRecipientRecord{EmailID: id, GroupID: "g"}))
		}
		return store, recipientsKV, ids
	}

	t.Run("returns only the requested page, in index order", func(t *testing.T) {
		t.Parallel()
		store, _, ids := seed(t, 25)
		var got []string
		total, err := store.ScanGroupRecords(context.Background(), "g", 10, 7, func(r api.EmailRecipientRecord) bool {
			got = append(got, r.EmailID)
			return true
		})
		require.NoError(t, err)
		assert.Equal(t, 25, total)
		assert.Equal(t, ids[10:17], got)
	})

	t.Run("offset past the end yields nothing", func(t *testing.T) {
		t.Parallel()
		store, _, _ := seed(t, 3)
		called := false
		total, err := store.ScanGroupRecords(context.Background(), "g", 3, 10, func(api.EmailRecipientRecord) bool {
			called = true
			return true
		})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.False(t, called)
	})

	t.Run("limit is capped at MaxGroupEmails", func(t *testing.T) {
		t.Parallel()
		store, recipientsKV, groupIndexKV := newStore(t)
		ids := seedFullGroup(t, groupIndexKV, "g", api.MaxGroupEmails+50)
		count := 0
		total, err := store.ScanGroupRecords(context.Background(), "g", 0, api.MaxGroupEmails*5, func(api.EmailRecipientRecord) bool {
			count++
			return true
		})
		require.NoError(t, err)
		assert.Equal(t, len(ids), total)
		assert.Equal(t, 0, count, "records are absent, nothing is yielded")
		assert.Len(t, recipientsKV.calledKeys(), api.MaxGroupEmails, "no more than MaxGroupEmails records are read")
	})

	t.Run("fn returning false stops the scan", func(t *testing.T) {
		t.Parallel()
		store, recipientsKV, _ := seed(t, 100)
		reads := len(recipientsKV.calledKeys())
		count := 0
		_, err := store.ScanGroupRecords(context.Background(), "g", 0, 100, func(api.EmailRecipientRecord) bool {
			count++
			return count < 3
		})
		require.NoError(t, err)
		assert.Equal(t, 3, count)
		assert.LessOrEqual(t, len(recipientsKV.calledKeys())-reads, 16, "only the first chunk is read")
	})

	t.Run("a done context stops the scan", func(t *testing.T) {
		t.Parallel()
		store, recipientsKV, _ := seed(t, 100)
		reads := len(recipientsKV.calledKeys())
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, err := store.ScanGroupRecords(ctx, "g", 0, 100, func(api.EmailRecipientRecord) bool { return true })
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, reads, len(recipientsKV.calledKeys()), "no record is read once the deadline has passed")
	})

	t.Run("a deadline expiring during the last chunk's reads ends the scan", func(t *testing.T) {
		t.Parallel()
		recipientsKV := newFakeBucket()
		groupIndexKV := newFakeBucket()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store := kvinfra.New(&cancelOnGet{fakeBucket: recipientsKV, cancel: cancel}, groupIndexKV)
		ids := seedFullGroup(t, groupIndexKV, "g", 3) // a single chunk
		for _, id := range ids {
			_, err := recipientsKV.Put(id, []byte(`{"email_id":"`+id+`","group_id":"g"}`))
			require.NoError(t, err)
		}

		called := false
		_, err := store.ScanGroupRecords(ctx, "g", 0, 10, func(api.EmailRecipientRecord) bool {
			called = true
			return true
		})
		require.ErrorIs(t, err, context.Canceled)
		assert.False(t, called, "no record is handed out after the deadline")
	})

	t.Run("a deadline expiring during the index read ends the scan", func(t *testing.T) {
		t.Parallel()
		groupIndexKV := newFakeBucket()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store := kvinfra.New(newFakeBucket(), &cancelOnGet{fakeBucket: groupIndexKV, cancel: cancel})
		seedFullGroup(t, groupIndexKV, "g", 3)

		// An offset past the end would otherwise be an empty success.
		_, err := store.ScanGroupRecords(ctx, "g", 3, 10, func(api.EmailRecipientRecord) bool { return true })
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("rejects an invalid range", func(t *testing.T) {
		t.Parallel()
		store, _, _ := seed(t, 1)
		for _, r := range [][2]int{{-1, 10}, {0, 0}, {0, -5}} {
			_, err := store.ScanGroupRecords(context.Background(), "g", r[0], r[1], func(api.EmailRecipientRecord) bool { return true })
			assert.Error(t, err, "offset %d limit %d", r[0], r[1])
		}
	})
}

func TestStore_RejectsInvalidKeysWithoutBucketCall(t *testing.T) {
	t.Parallel()

	invalid := map[string]string{
		"empty":        "",
		"oversized":    strings.Repeat("a", 257),
		"huge":         strings.Repeat("a", 100000),
		"bad chars":    "a b",
		"leading dot":  ".group",
		"trailing dot": "group.",
		"double dot":   "a..b",
	}
	for name, key := range invalid {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store, recipientsKV, groupIndexKV := newStore(t)
			ctx := context.Background()

			err := store.WriteRecord(ctx, key, api.EmailRecipientRecord{})
			assert.ErrorIs(t, err, kvinfra.ErrInvalidKey, "WriteRecord")

			_, err = store.GetRecord(ctx, key)
			assert.ErrorIs(t, err, kvinfra.ErrInvalidKey, "GetRecord")

			called := false
			err = store.UpdateRecord(ctx, key, func(_ *api.EmailRecipientRecord) { called = true })
			assert.ErrorIs(t, err, kvinfra.ErrInvalidKey, "UpdateRecord")
			assert.False(t, called)

			_, _, err = scanAll(ctx, store, key)
			assert.ErrorIs(t, err, kvinfra.ErrInvalidKey, "ScanGroupRecords")

			_, err = store.GroupExists(ctx, key)
			assert.ErrorIs(t, err, kvinfra.ErrInvalidKey, "GroupExists")

			err = store.AppendToGroup(ctx, key, uuid1)
			assert.ErrorIs(t, err, kvinfra.ErrInvalidKey, "AppendToGroup groupID")

			err = store.AppendToGroup(ctx, "g1", key)
			assert.ErrorIs(t, err, kvinfra.ErrInvalidKey, "AppendToGroup emailID")

			assert.Empty(t, recipientsKV.calledKeys(), "recipients bucket must not be called")
			assert.Empty(t, groupIndexKV.calledKeys(), "group index bucket must not be called")
		})
	}

	t.Run("error does not echo the raw key", func(t *testing.T) {
		t.Parallel()
		store, _, _ := newStore(t)
		key := strings.Repeat("z", 300)
		_, err := store.GetRecord(context.Background(), key)
		require.ErrorIs(t, err, kvinfra.ErrInvalidKey)
		assert.NotContains(t, err.Error(), key)
	})

	t.Run("accepts a key at the length bound", func(t *testing.T) {
		t.Parallel()
		store, _, _ := newStore(t)
		_, err := store.GetRecord(context.Background(), strings.Repeat("a", 256))
		assert.ErrorIs(t, err, domain.ErrNotFound)
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

	// A value rejected for its size fails the same way on every attempt, so it
	// must be reported as domain.ErrRecordTooLarge without a retry.
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"server max message size", &natsgo.APIError{Code: 400, ErrorCode: 10054, Description: "message size exceeds maximum allowed"}},
		{"client max payload", natsgo.ErrMaxPayload},
	} {
		t.Run("value too large is not retried: "+tc.name, func(t *testing.T) {
			t.Parallel()
			store, recipientsKV, _ := newStore(t)

			r := api.EmailRecipientRecord{EmailID: "e-big", GroupID: "g1", SentAt: time.Now().UTC()}
			require.NoError(t, store.WriteRecord(context.Background(), "e-big", r))
			recipientsKV.UpdateErr = tc.err

			err := store.UpdateRecord(context.Background(), "e-big", func(rec *api.EmailRecipientRecord) {
				rec.Delivered = true
			})
			require.ErrorIs(t, err, domain.ErrRecordTooLarge)
			// Put, then a single Get + Update: no retry.
			assert.Len(t, recipientsKV.calledKeys(), 3)
		})
	}

	t.Run("other update errors are retried and not classified as too large", func(t *testing.T) {
		t.Parallel()
		store, recipientsKV, _ := newStore(t)

		r := api.EmailRecipientRecord{EmailID: "e-down", GroupID: "g1", SentAt: time.Now().UTC()}
		require.NoError(t, store.WriteRecord(context.Background(), "e-down", r))
		recipientsKV.UpdateErr = errors.New("nats: timeout")

		err := store.UpdateRecord(context.Background(), "e-down", func(rec *api.EmailRecipientRecord) {
			rec.Delivered = true
		})
		require.Error(t, err)
		assert.NotErrorIs(t, err, domain.ErrRecordTooLarge)
		// Put, then two Get + Update attempts.
		assert.Len(t, recipientsKV.calledKeys(), 5)
	})
}

// Compile-time assertion: *kvinfra.Store satisfies domain.TrackingStore.
var _ domain.TrackingStore = (*kvinfra.Store)(nil)
