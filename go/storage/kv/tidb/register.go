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
		return New(cfg.DSN, table)
	})
}
