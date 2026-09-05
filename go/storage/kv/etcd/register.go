package etcd

import (
	"fmt"

	"hop.top/kit/go/storage/kv"
)

func init() {
	kv.RegisterBackend("etcd", func(cfg kv.Config) (kv.Store, error) {
		if len(cfg.Endpoints) == 0 {
			return nil, fmt.Errorf("kv: etcd backend requires Endpoints")
		}
		return New(cfg.Endpoints, cfg.Prefix)
	})
}
