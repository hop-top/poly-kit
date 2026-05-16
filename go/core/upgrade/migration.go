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
}

var (
	registryMu sync.Mutex
	registry   []Migration
)

// RegisterMigration adds a migration to the global registry.
// Typically called from init() functions in migration files.
func RegisterMigration(m Migration) {
	if m.Up == nil {
		panic("upgrade: migration " + m.Version + " has nil Up function")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, m)
}

// ResetRegistryForTest clears all registered migrations.
// Intended for testing only; do not use in production code.
func ResetRegistryForTest() {
	resetRegistry()
}

// resetRegistry clears all registered migrations (testing only).
func resetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = nil
}

// pendingMigrations returns migrations for schema where from < version <= to,
// ordered ascending by semver.
func pendingMigrations(schema, from, to string) []Migration {
	registryMu.Lock()
	defer registryMu.Unlock()

	fromV := parseSemver(strings.TrimPrefix(from, "v"))
	toV := parseSemver(strings.TrimPrefix(to, "v"))

	var pending []Migration
	for _, m := range registry {
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
