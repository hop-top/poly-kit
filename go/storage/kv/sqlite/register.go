package sqlite

import (
	"context"
	"fmt"

	"hop.top/kit/go/storage/kv"
)

func init() {
	kv.RegisterBackendContext("sqlite", func(ctx context.Context, cfg kv.Config) (kv.Store, error) {
		if cfg.Path == "" {
			return nil, fmt.Errorf("kv: sqlite backend requires Path")
		}
		return NewContext(ctx, cfg.Path)
	})
}
