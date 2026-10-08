// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package kv implements domain.TrackingStore on top of NATS JetStream KeyValue buckets.
package kv

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"

	natsgo "github.com/nats-io/nats.go"

	"github.com/linuxfoundation/lfx-v2-email-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-email-service/internal/logging"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
	"github.com/linuxfoundation/lfx-v2-email-service/pkg/redaction"
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

// ErrInvalidKey is returned when a key fails the store's boundary check. The
// check runs before any bucket call so an oversized or malformed key is never
// placed on the shared NATS connection.
var ErrInvalidKey = errors.New("invalid kv key")

// maxKeyLen bounds every key the store passes to a bucket. nats.go's keyValid
// bounds the character set but not the length, and a KV key ends up in the
// subject of the request nats.go sends (e.g. the direct-get subject). A key
// long enough to exceed the server's max_control_line (default 4096 bytes)
// makes the server close the service's single shared NATS connection. 256
// matches the group_id bound enforced by the service layer.
const maxKeyLen = 256

// jsErrCodeMessageTooLarge is the JetStream API error code nats-server returns
// when a published value exceeds the stream's max message size (the KV
// bucket's maxValueSize). nats.go v1.47.0 does not export a constant for it.
const jsErrCodeMessageTooLarge natsgo.ErrorCode = 10054

// isValueTooLarge reports whether err is a deterministic rejection of a value
// for its size: the server's max-message-size error, or the client's
// max-payload check. Retrying such a write with the same value cannot succeed.
func isValueTooLarge(err error) bool {
	if errors.Is(err, natsgo.ErrMaxPayload) {
		return true
	}
	var apiErr *natsgo.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode == jsErrCodeMessageTooLarge
}

// groupReadConcurrency is how many recipient records ScanGroupRecords reads
// at once. Records are read in chunks of this size, so at most this many are
// held in memory by a scan at any time.
const groupReadConcurrency = 16

// keyRe matches the NATS KV key character set accepted by nats.go keyValid.
var keyRe = regexp.MustCompile(`^[-/_=.a-zA-Z0-9]+$`)

// emailIDRe matches the canonical 8-4-4-4-12 UUID format (case-insensitive),
// mirroring isValidUUID in internal/service. Every legitimate group-index entry
// is a service-generated UUID.
var emailIDRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// checkKey returns ErrInvalidKey when key is empty, longer than maxKeyLen,
// contains characters outside the NATS KV key character set, starts or ends
// with '.', or contains consecutive dots. The dot rules mirror isValidGroupID
// in internal/service: nats.go keyValid rejects leading/trailing dots, and a
// ".." key yields an empty subject token that nats-server will not route. The
// raw key is deliberately not included in the error so callers can log it
// safely.
func checkKey(key string) error {
	if len(key) == 0 || len(key) > maxKeyLen || !keyRe.MatchString(key) ||
		key[0] == '.' || key[len(key)-1] == '.' || strings.Contains(key, "..") {
		return fmt.Errorf("%w (length %d)", ErrInvalidKey, len(key))
	}
	return nil
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
	if err := checkKey(emailID); err != nil {
		return err
	}
	if _, err := s.recipientsKV.Put(emailID, b); err != nil {
		return fmt.Errorf("kv put recipient record: %w", err)
	}
	return nil
}

// AppendToGroup appends emailID to the group's index entry using optimistic
// concurrency. It retries once on write conflict. If the group entry does not
// yet exist it is created. A failed read on an existing key aborts without
// writing so a transient Get error does not clobber the existing index. A group
// that already holds api.MaxGroupEmails entries is not written and
// domain.ErrGroupFull is returned, so the index value stays far below the
// bucket's maxValueSize.
func (s *Store) AppendToGroup(ctx context.Context, groupID, emailID string) error {
	if err := checkKey(groupID); err != nil {
		return err
	}
	if !emailIDRe.MatchString(emailID) {
		return fmt.Errorf("%w (length %d)", ErrInvalidKey, len(emailID))
	}
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
				slog.WarnContext(ctx, "corrupted group index, resetting", logging.ErrKey, jsonErr, "group_id", redaction.RedactGroupHandle(groupID))
				ids = nil
			}
		case errors.Is(err, natsgo.ErrKeyNotFound):
			isNew = true
		default:
			return fmt.Errorf("kv get group index: %w", err)
		}

		// A failed write may still have been applied; do not append twice.
		if attempt > 0 && slices.Contains(ids, emailID) {
			return nil
		}
		if len(ids) >= api.MaxGroupEmails {
			return fmt.Errorf("append to group index (%d entries): %w", len(ids), domain.ErrGroupFull)
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
			slog.DebugContext(ctx, "group index write conflict, retrying", "group_id", redaction.RedactGroupHandle(groupID))
		}
	}
	// The retry lost its write too. Re-read once so a group that concurrent
	// writers filled is reported as domain.ErrGroupFull (the send must not be
	// tracked under it), and a write that was applied despite its error is
	// reported as success.
	if entry, err := s.groupIndexKV.Get(groupID); err == nil {
		var ids []string
		if json.Unmarshal(entry.Value(), &ids) == nil {
			if slices.Contains(ids, emailID) {
				return nil
			}
			if len(ids) >= api.MaxGroupEmails {
				return fmt.Errorf("append to group index (%d entries): %w", len(ids), domain.ErrGroupFull)
			}
		}
	}
	return fmt.Errorf("kv update group index after retry: %w", writeErr)
}

// GroupExists reports whether the group index holds an entry for groupID. It
// returns (true, domain.ErrGroupFull) when the entry already lists
// api.MaxGroupEmails email IDs. An index value that cannot be decoded is
// reported as existing and not full; AppendToGroup resets it on the next write.
func (s *Store) GroupExists(_ context.Context, groupID string) (bool, error) {
	if err := checkKey(groupID); err != nil {
		return false, err
	}
	entry, err := s.groupIndexKV.Get(groupID)
	if err != nil {
		if errors.Is(err, natsgo.ErrKeyNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("kv get group index: %w", err)
	}
	var ids []string
	if json.Unmarshal(entry.Value(), &ids) == nil && len(ids) >= api.MaxGroupEmails {
		return true, domain.ErrGroupFull
	}
	return true, nil
}

// GetRecord retrieves the EmailRecipientRecord for emailID.
// Returns domain.ErrNotFound when the key does not exist.
func (s *Store) GetRecord(_ context.Context, emailID string) (api.EmailRecipientRecord, error) {
	if err := checkKey(emailID); err != nil {
		return api.EmailRecipientRecord{}, err
	}
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

// ScanGroupRecords resolves the group index entries at positions
// [offset, offset+limit) and calls fn, in index order, for each readable
// EmailRecipientRecord that belongs to groupID; fn returning false stops the
// scan. It returns the total number of email IDs recorded in the group index.
// Returns domain.ErrNotFound when the group index key does not exist.
//
// The work is bounded: limit is capped at api.MaxGroupEmails, records are read
// groupReadConcurrency at a time and handed to fn chunk by chunk (so at most
// one chunk of records is held here), and ctx is checked before every chunk,
// so a done context stops the scan with a wrapped ctx.Err().
//
// Individual recipient records that are absent or unreadable are skipped.
// Records whose GroupID does not match groupID are skipped. Index entries that
// are not UUIDs are skipped without any bucket call: the index value is stored
// data writable by any principal with publish rights on the bucket subject, so
// its entries are re-validated before use as KV keys.
func (s *Store) ScanGroupRecords(ctx context.Context, groupID string, offset, limit int, fn func(api.EmailRecipientRecord) bool) (int, error) {
	if err := checkKey(groupID); err != nil {
		return 0, err
	}
	if offset < 0 || limit <= 0 {
		return 0, fmt.Errorf("invalid group scan range (offset %d, limit %d)", offset, limit)
	}
	limit = min(limit, api.MaxGroupEmails)
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("scan group records: %w", err)
	}
	entry, err := s.groupIndexKV.Get(groupID)
	// The index read itself can outlast the deadline; observe that before any
	// outcome, including an empty window, is reported.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, fmt.Errorf("scan group records: %w", ctxErr)
	}
	if err != nil {
		if errors.Is(err, natsgo.ErrKeyNotFound) {
			return 0, domain.ErrNotFound
		}
		return 0, fmt.Errorf("kv get group index: %w", err)
	}

	var emailIDs []string
	if err := json.Unmarshal(entry.Value(), &emailIDs); err != nil {
		return 0, fmt.Errorf("unmarshal group index: %w", err)
	}

	totalIDs := len(emailIDs)
	if offset >= totalIDs {
		return totalIDs, nil
	}
	window := emailIDs[offset:min(totalIDs, offset+limit)]

	var (
		records                   [groupReadConcurrency]api.EmailRecipientRecord
		readErrs                  [groupReadConcurrency]error
		valid                     [groupReadConcurrency]bool
		invalidIDs, maxInvalidLen int
		foreignIDs, unreadableIDs int
		lastReadErr               error
		stopped                   bool
	)
	for start := 0; start < len(window) && !stopped; start += groupReadConcurrency {
		if err := ctx.Err(); err != nil {
			return totalIDs, fmt.Errorf("scan group records: %w", err)
		}
		chunk := window[start:min(start+groupReadConcurrency, len(window))]
		var wg sync.WaitGroup
		for i, emailID := range chunk {
			valid[i] = emailIDRe.MatchString(emailID)
			if !valid[i] {
				invalidIDs++
				maxInvalidLen = max(maxInvalidLen, len(emailID))
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				records[i], readErrs[i] = s.GetRecord(ctx, emailID)
			}()
		}
		wg.Wait()
		// Re-check after the reads: a deadline that expired while this chunk
		// (possibly the last one) was being read must still end the scan.
		if err := ctx.Err(); err != nil {
			return totalIDs, fmt.Errorf("scan group records: %w", err)
		}

		for i := range chunk {
			if !valid[i] {
				continue
			}
			if readErrs[i] != nil {
				unreadableIDs++
				lastReadErr = readErrs[i]
				continue
			}
			// A record is returned only if it was sent under this group, mirroring the
			// single-email check in GetEmailStatusHandler, so a stale or altered index
			// entry cannot pull another group's record into this group's results.
			if subtle.ConstantTimeCompare([]byte(records[i].GroupID), []byte(groupID)) != 1 {
				foreignIDs++
				continue
			}
			if !fn(records[i]) {
				stopped = true
				break
			}
		}
	}
	if unreadableIDs > 0 {
		// One aggregated line per scan, so a large group of missing records
		// cannot turn a single request into thousands of log lines.
		slog.WarnContext(ctx, "skipping unreadable recipient records during group lookup",
			"group_id", redaction.RedactGroupHandle(groupID), "unreadable_count", unreadableIDs, logging.ErrKey, lastReadErr)
	}
	if foreignIDs > 0 {
		slog.WarnContext(ctx, "skipping group index entries whose record belongs to another group",
			"group_id", redaction.RedactGroupHandle(groupID), "foreign_count", foreignIDs)
	}
	if invalidIDs > 0 {
		// Log counts and lengths only; the raw values are untrusted.
		slog.WarnContext(ctx, "skipping non-UUID email_ids in group index",
			"group_id", redaction.RedactGroupHandle(groupID), "invalid_count", invalidIDs, "max_invalid_len", maxInvalidLen)
	}
	// A scan never reports success once ctx is done, including when the
	// deadline expired while fn was handling the last records.
	if err := ctx.Err(); err != nil {
		return totalIDs, fmt.Errorf("scan group records: %w", err)
	}
	return totalIDs, nil
}

// UpdateRecord fetches the record for emailID, applies fn, and writes it back
// using optimistic concurrency. It retries once on write conflict. If the record
// does not exist, fn is not called and nil is returned (late-arriving SES events
// for unknown email IDs are expected and non-retryable). A write rejected because
// the value exceeds the bucket's size limit is not retried and is returned
// wrapping domain.ErrRecordTooLarge.
func (s *Store) UpdateRecord(ctx context.Context, emailID string, fn func(*api.EmailRecipientRecord)) error {
	if err := checkKey(emailID); err != nil {
		return err
	}
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
		if isValueTooLarge(lastUpdateErr) {
			return fmt.Errorf("kv update recipient record (%d bytes): %w: %w", len(updated), domain.ErrRecordTooLarge, lastUpdateErr)
		}
		if attempt == 0 {
			slog.DebugContext(ctx, "recipient record write conflict, retrying", "email_id", emailID)
		}
	}
	return fmt.Errorf("kv update recipient record after retry: %w", lastUpdateErr)
}
