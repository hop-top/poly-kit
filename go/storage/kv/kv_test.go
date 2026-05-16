package kv_test

import (
	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/badger"
	"hop.top/kit/go/storage/kv/etcd"
	"hop.top/kit/go/storage/kv/sqlite"
	"hop.top/kit/go/storage/kv/tidb"
)

// Compile-time interface assertions.
var (
	_ kv.Store    = (*sqlite.Store)(nil)
	_ kv.TTLStore = (*sqlite.Store)(nil)
	_ kv.Store    = (*badger.Store)(nil)
	_ kv.TTLStore = (*badger.Store)(nil)
	_ kv.Store    = (*etcd.Store)(nil)
	_ kv.Store    = (*tidb.Store)(nil)
)
