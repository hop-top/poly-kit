package badger

import (
	"fmt"

	"hop.top/kit/go/storage/kv"
)

func init() {
	kv.RegisterBackend("badger", func(cfg kv.Config) (kv.Store, error) {
		if cfg.Path == "" {
			return nil, fmt.Errorf("kv: badger backend requires Path")
		}
		return New(cfg.Path)
	})
}
