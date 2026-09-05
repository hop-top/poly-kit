package etcd

import (
	"context"
	"fmt"

	"hop.top/kit/go/storage/kv"
)

func init() {
	kv.RegisterBackendContext("etcd", func(ctx context.Context, cfg kv.Config) (kv.Store, error) {
		if len(cfg.Endpoints) == 0 {
			return nil, fmt.Errorf("kv: etcd backend requires Endpoints")
		}
		return NewContext(ctx, cfg.Endpoints, cfg.Prefix)
	})
}
