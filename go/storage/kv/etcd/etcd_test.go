package etcd_test

import (
	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/storage/kv/etcd"
)

// Compile-time interface assertion.
var _ kv.Store = (*etcd.Store)(nil)
