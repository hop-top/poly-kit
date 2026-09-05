// Package kv defines a minimal key-value storage abstraction.
//
// # Selecting a backend
//
// Open resolves Config.Backend against a registry that drivers populate from
// their own init functions. Importing a driver is what makes its name valid:
//
//	import (
//		"hop.top/kit/go/storage/kv"
//		_ "hop.top/kit/go/storage/kv/sqlite"
//	)
//
//	store, err := kv.Open(kv.Config{Backend: "sqlite", Path: "cache.db"})
//
// kit ships four drivers, each registering the matching backend name:
//
//	kv/sqlite    "sqlite"    file-backed, also a TTLStore
//	kv/badger    "badger"    directory-backed, also a TTLStore
//	kv/etcd      "etcd"      distributed etcd cluster
//	kv/tidb      "tidb"      TiDB or any MySQL-compatible server
//
// Registration is by import rather than by build tag so that a binary only
// carries the dependencies of the backends it actually opens. That matters
// most for etcd, which brings in gRPC, protobuf and zap; a program that only
// opens a SQLite file never links them. Open names the package to import
// when it is handed a known backend whose driver is absent, so the omission
// is self-correcting rather than silent.
package kv
