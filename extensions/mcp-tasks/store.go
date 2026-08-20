// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package tasks

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned by a Store when no record exists for a task
// ID — including records the store has discarded after their TTL
// elapsed, which SEP-2663 explicitly permits.
var ErrNotFound = errors.New("task not found")

// Store persists task records. The in-memory implementation returned
// by NewMemStore is the default; hosts that serve behind a load
// balancer must either route tasks/* requests to the instance holding
// the task (the Mcp-Name header exists for exactly that) or supply a
// shared implementation, because an in-memory store is per process.
//
// Implementations must be safe for concurrent use.
type Store interface {
	// Create durably persists a new record. It completes before the
	// CreateTaskResult is released to the client
	// (durable-before-respond), so eventually-consistent stores must
	// not return until a Get for the record's ID would succeed.
	Create(ctx context.Context, rec *Record) error

	// Get returns the record for taskID, or ErrNotFound.
	Get(ctx context.Context, taskID string) (*Record, error)

	// Mutate atomically applies fn to the record for taskID and
	// persists the outcome, returning the updated record. It returns
	// ErrNotFound for unknown IDs and fn's error unchanged when fn
	// fails (in which case nothing is persisted).
	Mutate(ctx context.Context, taskID string, fn func(*Record) error) (*Record, error)
}

// MemStore is the default in-memory Store. Records whose TTL has
// elapsed are pruned lazily on access, so an expired task answers
// ErrNotFound exactly like a never-created one.
type MemStore struct {
	mu   sync.Mutex
	recs map[string]*Record
	now  func() time.Time // test seam; time.Now outside tests
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{recs: make(map[string]*Record), now: time.Now}
}

// Create implements Store.
func (s *MemStore) Create(_ context.Context, rec *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if _, ok := s.recs[rec.TaskID]; ok {
		return errors.New("task already exists: " + rec.TaskID)
	}
	cp := *rec
	s.recs[rec.TaskID] = &cp
	return nil
}

// Get implements Store.
func (s *MemStore) Get(_ context.Context, taskID string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recs[taskID]
	if ok && rec.expired(s.now()) {
		delete(s.recs, taskID)
		ok = false
	}
	if !ok {
		return nil, ErrNotFound
	}
	cp := *rec
	return &cp, nil
}

// Mutate implements Store.
func (s *MemStore) Mutate(_ context.Context, taskID string, fn func(*Record) error) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recs[taskID]
	if ok && rec.expired(s.now()) {
		delete(s.recs, taskID)
		ok = false
	}
	if !ok {
		return nil, ErrNotFound
	}
	cp := *rec
	if err := fn(&cp); err != nil {
		return nil, err
	}
	s.recs[taskID] = &cp
	out := cp
	return &out, nil
}

// pruneLocked drops every expired record. Called with s.mu held.
func (s *MemStore) pruneLocked() {
	now := s.now()
	for id, rec := range s.recs {
		if rec.expired(now) {
			delete(s.recs, id)
		}
	}
}
