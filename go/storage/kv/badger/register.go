package badger

import (
	"context"
	"fmt"

	"hop.top/kit/go/storage/kv"
)

func init() {
	kv.RegisterBackendContext("badger", func(ctx context.Context, cfg kv.Config) (kv.Store, error) {
		if cfg.Path == "" {
			return nil, fmt.Errorf("kv: badger backend requires Path")
		}
		return NewContext(ctx, cfg.Path)
	})
}
