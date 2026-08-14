// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"errors"

	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// ErrNotFound is returned by TrackingStore when the requested key does not exist.
var ErrNotFound = errors.New("not found")

// TrackingStore is the interface for reading and writing email tracking records.
// All implementations must be safe for concurrent use.
//
// WriteRecord stores a new recipient record keyed by emailID.
//
// AppendToGroup appends emailID to the group's list (creating the list if absent)
// using optimistic concurrency — retries once on write conflict.
//
// GetRecord retrieves a recipient record by emailID; returns ErrNotFound when absent.
//
// GetGroupRecords returns all recipient records for a group_id; returns ErrNotFound
// when the group itself is absent, and silently skips individual records that have
// been deleted or have not yet been written.
//
// UpdateRecord fetches the record for emailID, applies fn in place, and writes it
// back with optimistic concurrency (one retry on conflict). If the record does not
// exist it returns nil without calling fn — expected for late-arriving SES events.
type TrackingStore interface {
	WriteRecord(ctx context.Context, emailID string, r api.EmailRecipientRecord) error
	AppendToGroup(ctx context.Context, groupID, emailID string) error
	GetRecord(ctx context.Context, emailID string) (api.EmailRecipientRecord, error)
	GetGroupRecords(ctx context.Context, groupID string) ([]api.EmailRecipientRecord, error)
	UpdateRecord(ctx context.Context, emailID string, fn func(*api.EmailRecipientRecord)) error
}

// NullTrackingStore is a no-op TrackingStore used when the NATS KV buckets are
// unavailable at startup. All writes succeed silently; all reads return ErrNotFound.
type NullTrackingStore struct{}

func (NullTrackingStore) WriteRecord(_ context.Context, _ string, _ api.EmailRecipientRecord) error {
	return nil
}

func (NullTrackingStore) AppendToGroup(_ context.Context, _, _ string) error {
	return nil
}

func (NullTrackingStore) GetRecord(_ context.Context, _ string) (api.EmailRecipientRecord, error) {
	return api.EmailRecipientRecord{}, ErrNotFound
}

func (NullTrackingStore) GetGroupRecords(_ context.Context, _ string) ([]api.EmailRecipientRecord, error) {
	return nil, ErrNotFound
}

func (NullTrackingStore) UpdateRecord(_ context.Context, _ string, _ func(*api.EmailRecipientRecord)) error {
	return nil
}
