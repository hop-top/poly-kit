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
