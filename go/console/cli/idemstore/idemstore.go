// Package idemstore provides the kit-managed idempotency-key store used
// by --idempotency-key replay middleware: the Store interface, its
// Result, the kit default TTL and an in-process Memory() backend for
// tests and adopters that do not want disk persistence.
//
// The persistent backend is a per-tool sqlite database at
// $XDG_STATE_HOME/<tool>/idempotency.db with a configurable TTL (kit
// default: 24 hours). It lives in go/console/cli/idemstore/sqlite, so
// a CLI that never opens it links no SQLite.
//
// Stored payloads are opaque to idemstore: callers serialize whatever
// envelope they need into Result.Output. The store does not redact
// secrets — that is the adopter's responsibility before
// recording.
//
// Persistence is per-tool: each adopter gets its own database file
// (XDG state dir / <tool> / idempotency.db). Cross-tool replay is
// out of scope by design.
package idemstore

import (
	"context"
	"sync"
	"time"
)

// Result is the recorded outcome of one idempotency-keyed invocation.
// Output is the opaque serialized envelope the RunE middleware
// captured (e.g. JSON output bytes); replay rewrites the same bytes
// back to the requesting stream.
type Result struct {
	// Key is the user-supplied idempotency key.
	Key string `json:"key"`
	// ExitCode is the process exit code recorded for the original
	// invocation (mirrors output.Error.ExitCode for failed runs).
	ExitCode int `json:"exit_code"`
	// Output is the opaque serialized output envelope. Replay writes
	// this to the requesting stream verbatim.
	Output []byte `json:"output"`
	// Recorded is when the original invocation completed and the
	// envelope was persisted.
	Recorded time.Time `json:"recorded"`
}

// Store is the kit-managed idempotency-key persistence interface.
// Lookup is a side-effect-free read; Record upserts the (key, result)
// tuple, replacing any prior record for the same key.
type Store interface {
	// Lookup returns the recorded result for key. Returns
	// (Result{}, false, nil) if the key is unknown or expired,
	// (Result{}, false, err) on backend errors.
	Lookup(ctx context.Context, key string) (Result, bool, error)
	// Record persists r under the given key. r.Recorded is set to
	// time.Now() when it is the zero value. Subsequent Records for
	// the same key overwrite the prior entry — the contract is
	// "last-write-wins" for replay payloads.
	Record(ctx context.Context, key string, r Result) error
	// Close releases backend resources. Calling Close on an
	// already-closed Store is safe.
	Close() error
}

// DefaultTTL is the kit default idempotency TTL (24h). After this
// much wall-clock time the recorded result is treated as missing on
// Lookup, and the sqlite Store deletes it (see
// hop.top/kit/go/console/cli/idemstore/sqlite).
const DefaultTTL = 24 * time.Hour

// Memory returns an in-process Store with no persistence. Suitable
// for tests and short-lived processes that want lookup-replay within
// a single run. The returned Store is safe for concurrent use.
//
// Memory() does not enforce a TTL — the stored map lives for the
// lifetime of the Store. Tests that exercise expiry should use the
// sqlite Store (idemstore/sqlite.Open) with an explicit ttl, or call
// Memory() and Close() per scenario.
func Memory() Store {
	return &memoryStore{m: make(map[string]Result), now: time.Now}
}

type memoryStore struct {
	mu sync.Mutex
	m  map[string]Result
	// now is the store's time source; time.Now outside tests.
	now func() time.Time
}

func (s *memoryStore) Lookup(_ context.Context, key string) (Result, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[key]
	if !ok {
		return Result{}, false, nil
	}
	return r, true, nil
}

func (s *memoryStore) Record(_ context.Context, key string, r Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Recorded.IsZero() {
		r.Recorded = s.now().UTC()
	}
	r.Key = key
	s.m[key] = r
	return nil
}

func (s *memoryStore) Close() error { return nil }
