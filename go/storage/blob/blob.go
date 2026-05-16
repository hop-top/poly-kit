package blob

import (
	"context"
	"io"
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
