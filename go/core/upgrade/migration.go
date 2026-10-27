package upgrade

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// Migration represents a versioned schema migration.
type Migration struct {
	Version string // semver incl. pre-release
	Schema  string // matches SchemaDriver.Name()
	Up      func(ctx context.Context) error
	Down    func(ctx context.Context) error // nil = no rollback
	// Verify, when set, runs after Up and before the version is
	// recorded. A failure counts as a failed step (ErrVerify) and the
	// rollback includes this migration.
	Verify func(ctx context.Context) error
}

// Registry holds migrations for one or more Migrators. The package-level
// RegisterMigration feeds DefaultRegistry, which a Migrator reads unless
// WithRegistry gives it another one.
type Registry struct {
	mu         sync.Mutex
	migrations []Migration
}

// NewRegistry returns an empty registry, for use with WithRegistry.
func NewRegistry() *Registry { return &Registry{} }

var defaultRegistry = NewRegistry()

// DefaultRegistry is the registry RegisterMigration adds to.
func DefaultRegistry() *Registry { return defaultRegistry }

// Register adds a migration. Panics when Up is nil.
func (r *Registry) Register(m Migration) {
	if m.Up == nil {
		panic("upgrade: migration " + m.Version + " has nil Up function")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.migrations = append(r.migrations, m)
}

func (r *Registry) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.migrations = nil
}

// RegisterMigration adds a migration to the default registry.
// Typically called from init() functions in migration files.
func RegisterMigration(m Migration) { defaultRegistry.Register(m) }

// ResetRegistryForTest clears the default registry.
// Intended for testing only; do not use in production code.
func ResetRegistryForTest() {
	resetRegistry()
}

// resetRegistry clears the default registry (testing only).
func resetRegistry() { defaultRegistry.reset() }

// pendingMigrations returns default-registry migrations for schema where
// from < version <= to, ordered ascending by semver.
func pendingMigrations(schema, from, to string) []Migration {
	return defaultRegistry.pending(schema, from, to)
}

// pending returns migrations for schema where from < version <= to,
// ordered ascending by semver.
func (r *Registry) pending(schema, from, to string) []Migration {
	r.mu.Lock()
	defer r.mu.Unlock()

	fromV := parseSemver(strings.TrimPrefix(from, "v"))
	toV := parseSemver(strings.TrimPrefix(to, "v"))

	var pending []Migration
	for _, m := range r.migrations {
		if m.Schema != schema {
			continue
		}
		mv := parseSemver(strings.TrimPrefix(m.Version, "v"))
		// from < version <= to
		if compareSemver(mv, fromV) > 0 && compareSemver(mv, toV) <= 0 {
			pending = append(pending, m)
		}
	}

	sort.Slice(pending, func(i, j int) bool {
		iv := parseSemver(strings.TrimPrefix(pending[i].Version, "v"))
		jv := parseSemver(strings.TrimPrefix(pending[j].Version, "v"))
		return compareSemver(iv, jv) < 0
	})

	return pending
}
