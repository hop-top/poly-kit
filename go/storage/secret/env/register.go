package env

import (
	"context"

	"hop.top/kit/go/storage/secret"
)

// readOnly wraps Store to satisfy MutableStore by erroring on writes.
// Lets env participate in secret.Open so callers can swap backends
// purely via config.
type readOnly struct{ *Store }

func (readOnly) Set(_ context.Context, _ string, _ []byte) error {
	return secret.ErrNotSupported
}

func (readOnly) Delete(_ context.Context, _ string) error {
	return secret.ErrNotSupported
}

func init() {
	secret.RegisterBackend("env", func(cfg secret.Config) (secret.MutableStore, error) {
		return readOnly{New(cfg.Prefix)}, nil
	})
}
