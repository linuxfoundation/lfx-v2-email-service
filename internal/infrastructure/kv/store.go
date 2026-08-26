// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package kv implements domain.TrackingStore on top of NATS JetStream KeyValue buckets.
package kv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// kvBucket is the subset of natsgo.KeyValue operations that Store needs.
// natsgo.KeyValue satisfies this interface in production; tests supply a small
// in-memory double without implementing the full vendor interface.
type kvBucket interface {
	Get(key string) (natsgo.KeyValueEntry, error)
	Put(key string, value []byte) (revision uint64, err error)
	Update(key string, value []byte, last uint64) (revision uint64, err error)
	Create(key string, value []byte) (revision uint64, err error)
}

// Store implements domain.TrackingStore using two NATS JetStream KV buckets:
// recipientsKV holds one EmailRecipientRecord per email_id, and groupIndexKV
// holds a JSON []string of email_ids per group_id.
type Store struct {
	recipientsKV kvBucket
	groupIndexKV kvBucket
}

// New creates a Store backed by the given KV buckets.
func New(recipientsKV, groupIndexKV kvBucket) *Store {
	return &Store{recipientsKV: recipientsKV, groupIndexKV: groupIndexKV}
}

// WriteRecord marshals r and puts it in the recipients bucket under emailID.
func (s *Store) WriteRecord(_ context.Context, emailID string, r api.EmailRecipientRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal recipient record: %w", err)
	}
	if _, err := s.recipientsKV.Put(emailID, b); err != nil {
		return fmt.Errorf("kv put recipient record: %w", err)
	}
	return nil
}

// AppendToGroup appends emailID to the group's index entry using optimistic
// concurrency. It retries once on write conflict. If the group entry does not
// yet exist it is created. A failed read on an existing key aborts without
// writing so a transient Get error does not clobber the existing index.
func (s *Store) AppendToGroup(ctx context.Context, groupID, emailID string) error {
	var writeErr error
	for attempt := range 2 {
		var ids []string
		var revision uint64
		var isNew bool

		entry, err := s.groupIndexKV.Get(groupID)
		switch {
		case err == nil:
			revision = entry.Revision()
			if jsonErr := json.Unmarshal(entry.Value(), &ids); jsonErr != nil {
				slog.WarnContext(ctx, "corrupted group index, resetting", logging.ErrKey, jsonErr, "group_id", groupID)
				ids = nil
			}
		case errors.Is(err, natsgo.ErrKeyNotFound):
			isNew = true
		default:
			return fmt.Errorf("kv get group index: %w", err)
		}

		ids = append(ids, emailID)
		b, _ := json.Marshal(ids)

		if isNew {
			_, writeErr = s.groupIndexKV.Create(groupID, b)
			if writeErr != nil && !errors.Is(writeErr, natsgo.ErrKeyExists) {
				return fmt.Errorf("kv create group index: %w", writeErr)
			}
		} else {
			_, writeErr = s.groupIndexKV.Update(groupID, b, revision)
		}

		if writeErr == nil {
			return nil
		}
		if attempt == 0 {
			slog.DebugContext(ctx, "group index write conflict, retrying", "group_id", groupID)
		}
	}
	return fmt.Errorf("kv update group index after retry: %w", writeErr)
}

// GetRecord retrieves the EmailRecipientRecord for emailID.
// Returns domain.ErrNotFound when the key does not exist.
func (s *Store) GetRecord(_ context.Context, emailID string) (api.EmailRecipientRecord, error) {
	entry, err := s.recipientsKV.Get(emailID)
	if err != nil {
		if errors.Is(err, natsgo.ErrKeyNotFound) {
			return api.EmailRecipientRecord{}, domain.ErrNotFound
		}
		return api.EmailRecipientRecord{}, fmt.Errorf("kv get recipient record: %w", err)
	}
	var r api.EmailRecipientRecord
	if err := json.Unmarshal(entry.Value(), &r); err != nil {
		return api.EmailRecipientRecord{}, fmt.Errorf("unmarshal recipient record: %w", err)
	}
	return r, nil
}

// GetGroupRecords returns all EmailRecipientRecord values belonging to groupID
// and the total number of email IDs recorded in the group index.
// Returns domain.ErrNotFound when the group index key does not exist.
// Individual recipient records that are absent or unreadable are silently
// skipped; the returned totalIDs reflects the raw index count regardless.
func (s *Store) GetGroupRecords(ctx context.Context, groupID string) ([]api.EmailRecipientRecord, int, error) {
	entry, err := s.groupIndexKV.Get(groupID)
	if err != nil {
		if errors.Is(err, natsgo.ErrKeyNotFound) {
			return nil, 0, domain.ErrNotFound
		}
		return nil, 0, fmt.Errorf("kv get group index: %w", err)
	}

	var emailIDs []string
	if err := json.Unmarshal(entry.Value(), &emailIDs); err != nil {
		return nil, 0, fmt.Errorf("unmarshal group index: %w", err)
	}

	totalIDs := len(emailIDs)
	records := make([]api.EmailRecipientRecord, 0, totalIDs)
	for _, emailID := range emailIDs {
		r, err := s.GetRecord(ctx, emailID)
		if err != nil {
			slog.WarnContext(ctx, "skipping unreadable recipient record during group lookup",
				"email_id", emailID, "group_id", groupID, logging.ErrKey, err)
			continue
		}
		records = append(records, r)
	}
	return records, totalIDs, nil
}

// UpdateRecord fetches the record for emailID, applies fn, and writes it back
// using optimistic concurrency. It retries once on write conflict. If the record
// does not exist, fn is not called and nil is returned (late-arriving SES events
// for unknown email IDs are expected and non-retryable).
func (s *Store) UpdateRecord(ctx context.Context, emailID string, fn func(*api.EmailRecipientRecord)) error {
	var lastUpdateErr error
	for attempt := range 2 {
		entry, err := s.recipientsKV.Get(emailID)
		if err != nil {
			if errors.Is(err, natsgo.ErrKeyNotFound) {
				return nil
			}
			return fmt.Errorf("kv get recipient record for update: %w", err)
		}

		var record api.EmailRecipientRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			return fmt.Errorf("unmarshal recipient record for update: %w", err)
		}

		fn(&record)

		updated, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("marshal updated recipient record: %w", err)
		}

		_, lastUpdateErr = s.recipientsKV.Update(emailID, updated, entry.Revision())
		if lastUpdateErr == nil {
			return nil
		}
		if attempt == 0 {
			slog.DebugContext(ctx, "recipient record write conflict, retrying", "email_id", emailID)
		}
	}
	return fmt.Errorf("kv update recipient record after retry: %w", lastUpdateErr)
}
