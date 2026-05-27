// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package mocks provides test doubles for external interfaces used by the service package.
package mocks

import (
	"errors"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"
)

// ErrWrongRevision is returned by KeyValue.Update when the supplied revision does not match.
var ErrWrongRevision = errors.New("wrong last revision")

// kvEntry implements natsgo.KeyValueEntry.
type kvEntry struct {
	key      string
	value    []byte
	revision uint64
}

func (e *kvEntry) Bucket() string               { return "mock" }
func (e *kvEntry) Key() string                  { return e.key }
func (e *kvEntry) Value() []byte                { return e.value }
func (e *kvEntry) Revision() uint64             { return e.revision }
func (e *kvEntry) Delta() uint64                { return 0 }
func (e *kvEntry) Created() time.Time           { return time.Time{} }
func (e *kvEntry) Operation() natsgo.KeyValueOp { return natsgo.KeyValuePut }

// KeyValue is a thread-safe in-memory mock that satisfies natsgo.KeyValue.
// Get, Put, Update, and PutString are functional. All other methods return an
// "not implemented" error — they exist only to satisfy the interface.
type KeyValue struct {
	mu        sync.RWMutex
	entries   map[string]*kvEntry
	PutErr    error            // if non-nil, Put returns this error
	GetErrFor map[string]error // if a key is present, Get returns that error instead of looking up the entry
}

// NewKeyValue returns an empty KeyValue mock.
func NewKeyValue() *KeyValue {
	return &KeyValue{entries: make(map[string]*kvEntry)}
}

func (m *KeyValue) Get(key string) (natsgo.KeyValueEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err, ok := m.GetErrFor[key]; ok {
		return nil, err
	}
	e, ok := m.entries[key]
	if !ok {
		return nil, natsgo.ErrKeyNotFound
	}
	return e, nil
}

func (m *KeyValue) Put(key string, value []byte) (uint64, error) {
	if m.PutErr != nil {
		return 0, m.PutErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rev := uint64(1)
	if e, ok := m.entries[key]; ok {
		rev = e.revision + 1
	}
	m.entries[key] = &kvEntry{key: key, value: append([]byte(nil), value...), revision: rev}
	return rev, nil
}

func (m *KeyValue) Update(key string, value []byte, last uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok || e.revision != last {
		return 0, ErrWrongRevision
	}
	rev := e.revision + 1
	m.entries[key] = &kvEntry{key: key, value: append([]byte(nil), value...), revision: rev}
	return rev, nil
}

func (m *KeyValue) GetRevision(_ string, _ uint64) (natsgo.KeyValueEntry, error) {
	return nil, errors.New("not implemented")
}
func (m *KeyValue) Create(_ string, _ []byte) (uint64, error) {
	return 0, errors.New("not implemented")
}
func (m *KeyValue) Delete(_ string, _ ...natsgo.DeleteOpt) error {
	return errors.New("not implemented")
}
func (m *KeyValue) Purge(_ string, _ ...natsgo.DeleteOpt) error {
	return errors.New("not implemented")
}
func (m *KeyValue) Watch(_ string, _ ...natsgo.WatchOpt) (natsgo.KeyWatcher, error) {
	return nil, errors.New("not implemented")
}
func (m *KeyValue) WatchAll(_ ...natsgo.WatchOpt) (natsgo.KeyWatcher, error) {
	return nil, errors.New("not implemented")
}
func (m *KeyValue) Keys(_ ...natsgo.WatchOpt) ([]string, error) {
	return nil, errors.New("not implemented")
}
func (m *KeyValue) History(_ string, _ ...natsgo.WatchOpt) ([]natsgo.KeyValueEntry, error) {
	return nil, errors.New("not implemented")
}
func (m *KeyValue) Bucket() string { return "mock" }
func (m *KeyValue) PurgeDeletes(_ ...natsgo.PurgeOpt) error {
	return errors.New("not implemented")
}
func (m *KeyValue) PutString(key string, value string) (uint64, error) {
	return m.Put(key, []byte(value))
}
func (m *KeyValue) WatchFiltered(_ []string, _ ...natsgo.WatchOpt) (natsgo.KeyWatcher, error) {
	return nil, errors.New("not implemented")
}
func (m *KeyValue) ListKeys(_ ...natsgo.WatchOpt) (natsgo.KeyLister, error) {
	return nil, errors.New("not implemented")
}
func (m *KeyValue) Status() (natsgo.KeyValueStatus, error) { return nil, errors.New("not implemented") }
