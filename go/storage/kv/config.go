package kv

import (
	"fmt"
	"sort"
	"strings"
)

// Config describes which backend to use.
//
// Fields are backend-specific; each backend documents which it reads and
// rejects a Config that omits what it needs. The zero value is not usable —
// Backend is always required.
type Config struct {
	// Backend names the driver to open: "sqlite", "badger", "etcd" or
	// "tidb". A name becomes valid once its driver package is imported
	// (see the package doc); Backends lists what is registered.
	Backend string

	// Path is the database file (sqlite) or directory (badger).
	Path string

	// Endpoints lists the etcd cluster client URLs.
	Endpoints []string

	// Prefix namespaces every key written by the etcd backend.
	Prefix string

	// DSN is the MySQL-compatible connection string for tidb.
	DSN string

	// Table is the tidb table holding the key-value rows. Defaults to
	// DefaultTable when empty.
	Table string
}

// DefaultTable is the tidb table name used when Config.Table is empty.
const DefaultTable = "kv"

// Opener is a function that creates a Store from config.
// Registered via RegisterBackend.
type Opener func(cfg Config) (Store, error)

var backends = map[string]Opener{}

// RegisterBackend registers a factory for the named backend.
//
// Drivers call this from an init function in their own package, so importing
// a driver is what makes its name valid for Open. Backends whose dependencies
// are costly (etcd pulls in gRPC and protobuf) therefore stay out of binaries
// that never ask for them.
func RegisterBackend(name string, fn Opener) {
	backends[name] = fn
}

// Backends returns the registered backend names in sorted order.
func Backends() []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Open creates a Store from config using registered backends.
//
// An unregistered name reports which names are available and, for the
// drivers kit ships, which package to import to get the missing one.
func Open(cfg Config) (Store, error) {
	fn, ok := backends[cfg.Backend]
	if !ok {
		if pkg, known := driverPackages[cfg.Backend]; known {
			return nil, fmt.Errorf(
				"kv: backend %q registered by %s; import that package to enable it (available: %s)",
				cfg.Backend, pkg, strings.Join(Backends(), ", "))
		}
		return nil, fmt.Errorf("kv: unknown backend %q (available: %s)",
			cfg.Backend, strings.Join(Backends(), ", "))
	}
	return fn(cfg)
}

// driverPackages maps the backends kit ships to the import path that
// registers each, so an unimported driver names its own remedy instead of
// looking like a typo.
var driverPackages = map[string]string{
	"sqlite": "hop.top/kit/go/storage/kv/sqlite",
	"badger": "hop.top/kit/go/storage/kv/badger",
	"etcd":   "hop.top/kit/go/storage/kv/etcd",
	"tidb":   "hop.top/kit/go/storage/kv/tidb",
}
