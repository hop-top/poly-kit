package memory

import (
	"context"

	"hop.top/kit/go/storage/kv"
)

func init() {
	kv.RegisterBackendContext("memory", func(context.Context, kv.Config) (kv.Store, error) {
		return New(), nil
	})
}
