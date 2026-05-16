package kv

import (
	"fmt"

	"hop.top/kit/go/storage/kv/badger"
	"hop.top/kit/go/storage/kv/sqlite"
)

// Config describes which backend to use.
type Config struct {
	Backend string // "sqlite", "badger", "etcd", "tidb"
	Path    string // for sqlite/badger (file path)
	DSN     string // for etcd/tidb (connection string)
}

// Open creates a Store from config.
func Open(cfg Config) (Store, error) {
	switch cfg.Backend {
	case "sqlite":
		if cfg.Path == "" {
			return nil, fmt.Errorf("kv: sqlite backend requires Path")
		}
		return sqlite.New(cfg.Path)
	case "badger":
		if cfg.Path == "" {
			return nil, fmt.Errorf("kv: badger backend requires Path")
		}
		return badger.New(cfg.Path)
	case "etcd":
		return nil, fmt.Errorf("kv: etcd backend not available (requires build tag)")
	case "tidb":
		return nil, fmt.Errorf("kv: tidb backend not available (requires build tag)")
	default:
		return nil, fmt.Errorf("kv: unknown backend %q", cfg.Backend)
	}
}
