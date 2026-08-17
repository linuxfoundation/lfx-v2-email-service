// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package mocks

import (
	"context"
	"sync"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// TrackingStore is a thread-safe in-memory mock that satisfies domain.TrackingStore.
// Construct with NewTrackingStore and pre-seed records with PutRecord / PutGroup.
// Inject errors via WriteErr, AppendErr, GetErrFor, and GroupErrFor.
//
// GetGroupRecords skips any per-record error (including those injected via GetErrFor),
// matching the best-effort fan-out semantics of kv.Store.
type TrackingStore struct {
	mu          sync.RWMutex
	records     map[string]api.EmailRecipientRecord
	groups      map[string][]string
	WriteErr    error            // if non-nil, WriteRecord returns this error
	AppendErr   error            // if non-nil, AppendToGroup returns this error
	GetErrFor   map[string]error // per-emailID error override for GetRecord / UpdateRecord / GetGroupRecords fan-out
	GroupErrFor map[string]error // per-groupID error override for GetGroupRecords (before fan-out)
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
	m.groups[groupID] = append(m.groups[groupID], emailID)
	return nil
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

// GetGroupRecords returns records for all email_ids in the group index and the
// total number of IDs in that index.
// Returns domain.ErrNotFound when the group itself is absent.
// All per-record errors (absent records, injected errors via GetErrFor, etc.)
// are silently skipped; totalIDs reflects the raw index count.
func (m *TrackingStore) GetGroupRecords(ctx context.Context, groupID string) ([]api.EmailRecipientRecord, int, error) {
	if err, ok := m.GroupErrFor[groupID]; ok {
		return nil, 0, err
	}
	m.mu.RLock()
	ids, ok := m.groups[groupID]
	if !ok {
		m.mu.RUnlock()
		return nil, 0, domain.ErrNotFound
	}
	idsCopy := make([]string, len(ids))
	copy(idsCopy, ids)
	m.mu.RUnlock()

	totalIDs := len(idsCopy)
	out := make([]api.EmailRecipientRecord, 0, totalIDs)
	for _, id := range idsCopy {
		r, err := m.GetRecord(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, totalIDs, nil
}

func (m *TrackingStore) UpdateRecord(_ context.Context, emailID string, fn func(*api.EmailRecipientRecord)) error {
	if err, ok := m.GetErrFor[emailID]; ok {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[emailID]
	if !ok {
		return nil // no record — drop silently, same as kv.Store
	}
	fn(&r)
	m.records[emailID] = r
	return nil
}
