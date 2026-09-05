package tidb

import (
	"fmt"

	"hop.top/kit/go/storage/kv"
)

func init() {
	kv.RegisterBackend("tidb", func(cfg kv.Config) (kv.Store, error) {
		if cfg.DSN == "" {
			return nil, fmt.Errorf("kv: tidb backend requires DSN")
		}
		table := cfg.Table
		if table == "" {
			table = kv.DefaultTable
		}
		// kv.Opener carries no context, so the open-time ping cannot be
		// policy-checked from here; widening Opener would break every
		// driver and adopter. New still installs the guarded dial hook,
		// so every query through the returned Store is covered.
		return New(cfg.DSN, table)
	})
}
