// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mocks

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// TrackingStore is a thread-safe in-memory mock that satisfies domain.TrackingStore.
// Construct with NewTrackingStore and pre-seed records with PutRecord / PutGroup.
// Inject errors via WriteErr, AppendErr, GetErrFor, and GroupErrFor.
//
// ScanGroupRecords skips any per-record error (including those injected via GetErrFor),
// matching the best-effort fan-out semantics of kv.Store. AppendToGroup and
// GroupExists enforce api.MaxGroupEmails with domain.ErrGroupFull, like kv.Store.
type TrackingStore struct {
	mu          sync.RWMutex
	records     map[string]api.EmailRecipientRecord
	groups      map[string][]string
	WriteErr    error            // if non-nil, WriteRecord returns this error
	AppendErr   error            // if non-nil, AppendToGroup returns this error
	GetErrFor   map[string]error // per-emailID error override for GetRecord / UpdateRecord / ScanGroupRecords fan-out
	GroupErrFor map[string]error // per-groupID error override for GroupExists and ScanGroupRecords (before fan-out)
}

// NewTrackingStore returns an empty TrackingStore mock.
func NewTrackingStore() *TrackingStore {
	return &TrackingStore{
		records: make(map[string]api.EmailRecipientRecord),
		groups:  make(map[string][]string),
	}
}

// PutRecord pre-seeds a recipient record (for use in test setup).
func (m *TrackingStore) PutRecord(emailID string, r api.EmailRecipientRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[emailID] = r
}

// PutGroup pre-seeds a group index entry (for use in test setup).
func (m *TrackingStore) PutGroup(groupID string, emailIDs []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, len(emailIDs))
	copy(ids, emailIDs)
	m.groups[groupID] = ids
}

// GetStoredRecord returns the record currently held for emailID (for assertions).
func (m *TrackingStore) GetStoredRecord(emailID string) (api.EmailRecipientRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.records[emailID]
	return r, ok
}

// GetStoredGroup returns the group index currently held for groupID (for assertions).
func (m *TrackingStore) GetStoredGroup(groupID string) ([]string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids, ok := m.groups[groupID]
	if !ok {
		return nil, false
	}
	out := make([]string, len(ids))
	copy(out, ids)
	return out, true
}

func (m *TrackingStore) WriteRecord(_ context.Context, emailID string, r api.EmailRecipientRecord) error {
	if m.WriteErr != nil {
		return m.WriteErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[emailID] = r
	return nil
}

func (m *TrackingStore) AppendToGroup(_ context.Context, groupID, emailID string) error {
	if m.AppendErr != nil {
		return m.AppendErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.groups[groupID]) >= api.MaxGroupEmails {
		return domain.ErrGroupFull
	}
	m.groups[groupID] = append(m.groups[groupID], emailID)
	return nil
}

func (m *TrackingStore) GroupExists(_ context.Context, groupID string) (bool, error) {
	if err, ok := m.GroupErrFor[groupID]; ok {
		return false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids, ok := m.groups[groupID]
	if ok && len(ids) >= api.MaxGroupEmails {
		return true, domain.ErrGroupFull
	}
	return ok, nil
}

func (m *TrackingStore) GetRecord(_ context.Context, emailID string) (api.EmailRecipientRecord, error) {
	if err, ok := m.GetErrFor[emailID]; ok {
		return api.EmailRecipientRecord{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.records[emailID]
	if !ok {
		return api.EmailRecipientRecord{}, domain.ErrNotFound
	}
	return r, nil
}

// ScanGroupRecords calls fn, in index order, for the records at index positions
// [offset, offset+limit) (limit capped at api.MaxGroupEmails) and returns the
// total number of IDs in the group index.
// Returns domain.ErrNotFound when the group itself is absent.
// All per-record errors (absent records, injected errors via GetErrFor, etc.)
// and records belonging to another group are silently skipped, matching
// kv.Store. A done ctx stops the scan with a wrapped ctx.Err(), checked after
// the group lookup (so an empty window still reports it), before each record,
// and before returning success.
func (m *TrackingStore) ScanGroupRecords(ctx context.Context, groupID string, offset, limit int, fn func(api.EmailRecipientRecord) bool) (int, error) {
	if err, ok := m.GroupErrFor[groupID]; ok {
		return 0, err
	}
	if offset < 0 || limit <= 0 {
		return 0, fmt.Errorf("invalid group scan range (offset %d, limit %d)", offset, limit)
	}
	m.mu.RLock()
	ids, ok := m.groups[groupID]
	if !ok {
		m.mu.RUnlock()
		return 0, domain.ErrNotFound
	}
	idsCopy := make([]string, len(ids))
	copy(idsCopy, ids)
	m.mu.RUnlock()

	totalIDs := len(idsCopy)
	// Like kv.Store, a done ctx is reported even when the window is empty.
	if err := ctx.Err(); err != nil {
		return totalIDs, fmt.Errorf("scan group records: %w", err)
	}
	if offset >= totalIDs {
		return totalIDs, nil
	}
	for _, id := range idsCopy[offset:min(totalIDs, offset+min(limit, api.MaxGroupEmails))] {
		if err := ctx.Err(); err != nil {
			return totalIDs, fmt.Errorf("scan group records: %w", err)
		}
		r, err := m.GetRecord(ctx, id)
		if err != nil || r.GroupID != groupID {
			continue
		}
		if !fn(r) {
			break
		}
	}
	// Like kv.Store, never report success once ctx is done, including when it
	// was cancelled during the last fn call.
	if err := ctx.Err(); err != nil {
		return totalIDs, fmt.Errorf("scan group records: %w", err)
	}
	return totalIDs, nil
}

func (m *TrackingStore) UpdateRecord(_ context.Context, emailID string, fn func(*api.EmailRecipientRecord)) error {
	if err, ok := m.GetErrFor[emailID]; ok {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.records[emailID]
	if !ok {
		return nil // no record — drop silently, same as kv.Store
	}
	// Like kv.Store, fn works on a private copy, so a failed update leaves the
	// stored record unchanged.
	r := cloneRecord(stored)
	fn(&r)
	// Like kv.Store, a record that cannot be serialised is not stored.
	if _, err := json.Marshal(r); err != nil {
		return fmt.Errorf("marshal updated recipient record: %w: %w", domain.ErrRecordUnencodable, err)
	}
	m.records[emailID] = r
	return nil
}

// cloneRecord returns a copy of r that shares no slices or pointers with it.
func cloneRecord(r api.EmailRecipientRecord) api.EmailRecipientRecord {
	r.OpenedAtList = slices.Clone(r.OpenedAtList)
	r.ClickEventIDs = slices.Clone(r.ClickEventIDs)
	r.ClickList = slices.Clone(r.ClickList)
	for _, p := range []**time.Time{&r.DeliveredAt, &r.LastOpenedAt, &r.LastClickedAt, &r.FailedAt} {
		if *p != nil {
			t := **p
			*p = &t
		}
	}
	return r
}
