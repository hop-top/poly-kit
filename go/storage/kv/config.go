package kv

import (
	"context"
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
//
// It carries no context, so a driver registered this way cannot consult the
// network policy while it connects. Prefer ContextOpener; this shape stays
// for drivers written against it.
type Opener func(cfg Config) (Store, error)

// ContextOpener is a function that creates a Store from config, honoring
// the policy carried by ctx. Registered via RegisterBackendContext.
//
// It exists because the offline marker (netpolicy.WithOffline) travels on a
// context: an Opener has none to consult, so its initial connect escapes the
// policy even when the driver's later queries are guarded. A ContextOpener
// closes that one-connect gap.
type ContextOpener func(ctx context.Context, cfg Config) (Store, error)

var (
	backends    = map[string]Opener{}
	ctxBackends = map[string]ContextOpener{}
)

// RegisterBackend registers a factory for the named backend.
//
// Drivers call this from an init function in their own package, so importing
// a driver is what makes its name valid for Open. Backends whose dependencies
// are costly (etcd pulls in gRPC and protobuf) therefore stay out of binaries
// that never ask for them.
//
// Prefer RegisterBackendContext for any driver that touches the network: an
// Opener cannot see the offline marker, so OpenContext has to fall back to
// opening it with a context-free call.
func RegisterBackend(name string, fn Opener) {
	backends[name] = fn
}

// RegisterBackendContext registers a context-aware factory for the named
// backend, alongside rather than instead of RegisterBackend.
//
// OpenContext prefers a factory registered here and falls back to a plain
// Opener when a driver offers none, so registering both is not required and
// a driver written against Opener keeps working unchanged. Open, which has no
// context of its own, always goes through the context-free path.
//
// A driver registered here is reported by Backends the same way, so
// registering only a ContextOpener is enough to make a name valid.
func RegisterBackendContext(name string, fn ContextOpener) {
	ctxBackends[name] = fn
}

// Backends returns the registered backend names in sorted order.
func Backends() []string {
	names := make([]string, 0, len(backends)+len(ctxBackends))
	for name := range backends {
		names = append(names, name)
	}
	for name := range ctxBackends {
		if _, dup := backends[name]; !dup {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Open creates a Store from config using registered backends.
//
// An unregistered name reports which names are available and, for the
// drivers kit ships, which package to import to get the missing one.
//
// Open has no context to police the connect with. A caller holding one
// should use OpenContext, which does.
func Open(cfg Config) (Store, error) {
	return OpenContext(context.Background(), cfg)
}

// OpenContext creates a Store from config using registered backends,
// honoring the network policy carried by ctx.
//
// It prefers a factory registered by RegisterBackendContext, which can refuse
// the initial connect on an offline-marked context. A backend that registered
// only a plain Opener falls back to that; such a driver connects without
// seeing the policy, which is the compatibility cost of not breaking Opener.
func OpenContext(ctx context.Context, cfg Config) (Store, error) {
	if fn, ok := ctxBackends[cfg.Backend]; ok {
		return fn(ctx, cfg)
	}
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
