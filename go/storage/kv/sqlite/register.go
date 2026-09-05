package sqlite

import (
	"fmt"

	"hop.top/kit/go/storage/kv"
)

func init() {
	kv.RegisterBackend("sqlite", func(cfg kv.Config) (kv.Store, error) {
		if cfg.Path == "" {
			return nil, fmt.Errorf("kv: sqlite backend requires Path")
		}
		return New(cfg.Path)
	})
}
