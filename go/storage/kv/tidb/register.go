package tidb

import (
	"context"
	"fmt"

	"hop.top/kit/go/storage/kv"
)

func init() {
	kv.RegisterBackendContext("tidb", func(ctx context.Context, cfg kv.Config) (kv.Store, error) {
		if cfg.DSN == "" {
			return nil, fmt.Errorf("kv: tidb backend requires DSN")
		}
		table := cfg.Table
		if table == "" {
			table = kv.DefaultTable
		}
		// NewContext checks the open-time ping against the policy on ctx,
		// so kv.OpenContext refuses an offline remote connect here rather
		// than only on the first query afterwards.
		return NewContext(ctx, cfg.DSN, table)
	})
}
