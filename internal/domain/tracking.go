// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"

	"github.com/linuxfoundation/lfx-v2-email-service/pkg/api"
)

// ErrNotFound is returned by TrackingStore when the requested key does not exist.
var ErrNotFound = errors.New("not found")

// ErrTrackingUnavailable is returned by NullTrackingStore.AppendToGroup: with no
// KV there is no group index, so a newly issued group handle cannot be recorded
// and must not be returned to the caller.
var ErrTrackingUnavailable = errors.New("tracking unavailable")

// groupHandleRe matches a service-issued group handle: the "grp_" prefix
// followed by 32 lowercase hex characters (128 bits from crypto/rand).
var groupHandleRe = regexp.MustCompile(`^grp_[0-9a-f]{32}$`)

// NewGroupHandle returns a new service-issued group handle.
//
// The group handle is the only credential that grants access to a group's
// tracking data (status, analytics, and appending further sends), so it must
// be unguessable, must only ever be issued by this service, and must not be
// disclosed outside the send_email reply (not in outbound mail headers, not in
// push events). The distinct "grp_" format keeps typical caller-chosen group_id
// values written before handles were introduced from being accepted as handles.
// The format alone does not prove provenance: earlier validation allowed a
// caller to choose an exact "grp_" + 32 hex key, which is why the rollout
// requires checking email-group-index for such keys first.
func NewGroupHandle() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error (Go 1.24+).
	return "grp_" + hex.EncodeToString(b[:])
}

// IsGroupHandle reports whether s has the format of a service-issued group handle.
func IsGroupHandle(s string) bool {
	return groupHandleRe.MatchString(s)
}

// ErrRecordTooLarge is returned by TrackingStore.UpdateRecord when the updated
// record is rejected because it exceeds the store's maximum value size. The
// failure is deterministic for that record, so callers must not retry it.
var ErrRecordTooLarge = errors.New("record exceeds maximum value size")

// TrackingStore is the interface for reading and writing email tracking records.
// All implementations must be safe for concurrent use.
//
// WriteRecord stores a new recipient record keyed by emailID.
//
// AppendToGroup appends emailID to the group's list (creating the list if absent)
// using optimistic concurrency — retries once on write conflict.
//
// GroupExists reports whether a group index entry exists for groupID. The send
// handler uses it to accept a caller-supplied group_id only when it names a
// group this service issued and recorded.
//
// GetRecord retrieves a recipient record by emailID; returns ErrNotFound when absent.
//
// GetGroupRecords returns all readable recipient records for a group_id and the
// total number of email IDs in the group index. Returns ErrNotFound when the
// group itself is absent. Individual records that are absent or unreadable are
// silently skipped; totalIDs always reflects the raw index count.
//
// UpdateRecord fetches the record for emailID, applies fn in place, and writes it
// back with optimistic concurrency (one retry on conflict). If the record does not
// exist it returns nil without calling fn — expected for late-arriving SES events.
// If the updated record is too large to store it returns ErrRecordTooLarge.
type TrackingStore interface {
	WriteRecord(ctx context.Context, emailID string, r api.EmailRecipientRecord) error
	AppendToGroup(ctx context.Context, groupID, emailID string) error
	GroupExists(ctx context.Context, groupID string) (bool, error)
	GetRecord(ctx context.Context, emailID string) (api.EmailRecipientRecord, error)
	GetGroupRecords(ctx context.Context, groupID string) (records []api.EmailRecipientRecord, totalIDs int, err error)
	UpdateRecord(ctx context.Context, emailID string, fn func(*api.EmailRecipientRecord)) error
}

// NullTrackingStore is a no-op TrackingStore used when the NATS KV buckets are
// unavailable at startup. Record writes succeed silently, AppendToGroup returns
// ErrTrackingUnavailable, and all reads return ErrNotFound.
type NullTrackingStore struct{}

func (NullTrackingStore) WriteRecord(_ context.Context, _ string, _ api.EmailRecipientRecord) error {
	return nil
}

func (NullTrackingStore) AppendToGroup(_ context.Context, _, _ string) error {
	return ErrTrackingUnavailable
}

// GroupExists reports true: with no KV there is no group index to check, and
// nothing is stored or readable, so accepting a well-formed handle (issued
// before tracking became unavailable) exposes no data while keeping sends to
// existing groups working. Documented as the degraded-mode exception.
func (NullTrackingStore) GroupExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (NullTrackingStore) GetRecord(_ context.Context, _ string) (api.EmailRecipientRecord, error) {
	return api.EmailRecipientRecord{}, ErrNotFound
}

func (NullTrackingStore) GetGroupRecords(_ context.Context, _ string) ([]api.EmailRecipientRecord, int, error) {
	return nil, 0, ErrNotFound
}

func (NullTrackingStore) UpdateRecord(_ context.Context, _ string, _ func(*api.EmailRecipientRecord)) error {
	return nil
}
