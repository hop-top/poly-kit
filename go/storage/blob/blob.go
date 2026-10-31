package blob

import (
	"context"
	"fmt"
	"io"
	"io/fs"
)

// Object holds metadata about a stored blob.
type Object struct {
	Key         string
	Size        int64
	ContentType string
}

// Store is the interface that blob storage backends must implement.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]Object, error)
	Exists(ctx context.Context, key string) (bool, error)
}

// Stater is an optional capability of a Store: report the metadata of one
// object, including its content type, without listing.
//
// It is deliberately not part of Store, so adapters that cannot or need not
// support it are not forced to. Callers type-assert:
//
//	if s, ok := store.(blob.Stater); ok {
//		obj, err := s.Stat(ctx, key)
//	}
//
// or use [Stat], which falls back when the capability is absent.
//
// Stat returns the same Object shape List does, but ContentType is the
// exact type of the latest Put (empty when none was given). A missing key
// returns an error, matching Get (local: errors.Is(err, fs.ErrNotExist)).
type Stater interface {
	Stat(ctx context.Context, key string) (Object, error)
}

// Stat returns metadata for key. It uses store's Stater when implemented.
// Otherwise it falls back to List(prefix=key) and picks the exact-key
// match; there ContentType is only as reliable as List's (best-effort, and
// possibly empty) and the cost is a listing of every key sharing the
// prefix. A missing key wraps fs.ErrNotExist in the fallback.
func Stat(ctx context.Context, store Store, key string) (Object, error) {
	if s, ok := store.(Stater); ok {
		return s.Stat(ctx, key)
	}
	objs, err := store.List(ctx, key)
	if err != nil {
		return Object{}, err
	}
	for _, o := range objs {
		if o.Key == key {
			return o, nil
		}
	}
	return Object{}, fmt.Errorf("blob: stat %s: %w", key, fs.ErrNotExist)
}
