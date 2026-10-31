package kv

import (
	"context"
	"time"
)

// Store is a minimal key-value storage interface.
type Store interface {
	Put(ctx context.Context, key string, value []byte) error
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
	Close() error
}

// TTLStore extends Store with time-to-live support.
type TTLStore interface {
	Store
	PutWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

// TTLInserter is the atomic "claim a key" operation a dedupe store
// needs: a get followed by a put lets two callers both see the key
// absent. Optional beside [TTLStore]; a backend that cannot make the
// claim atomic does not implement it.
type TTLInserter interface {
	TTLStore
	// PutIfAbsentWithTTL stores value under key, expiring after ttl, only
	// if no live entry holds the key, and reports whether it did. An
	// expired entry counts as absent. When it returns false the existing
	// entry is left untouched.
	PutIfAbsentWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) (inserted bool, err error)
}
