package config

import (
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// Migration transforms a config file from one schema version to the next.
// Migrations are versioned by integer; gaps are allowed but discouraged.
//
// Apply receives the parsed YAML as a generic map. It must return a new map
// representing the post-migration shape (or the same map mutated in place).
// Returning an error aborts the chain and propagates to the caller.
type Migration struct {
	From  int
	To    int
	Apply func(raw map[string]any) (map[string]any, error)
	// Note is a short human-readable description ("rename auth.api_key to
	// auth.token") shown by OnMigration hooks.
	Note string
}

// MigrateOptions controls Migrate (and the Migrations applied during Load).
type MigrateOptions struct {
	// SchemaKey is the dotted top-level key holding the current version.
	// Default "schema_version" when empty. The key MUST live at the root of
	// the YAML document; nested schema_version fields are not supported.
	SchemaKey string
	// WriteBack, when true, re-serializes the migrated map and overwrites
	// the source file. Off by default to avoid silent disk rewrites.
	WriteBack bool
	// OnMigration, when non-nil, is invoked once per migration applied,
	// after Apply has succeeded. Useful for logging and dry-run output.
	OnMigration func(path string, m Migration)
}

func schemaKeyOrDefault(k string) string {
	if k == "" {
		return "schema_version"
	}
	return k
}

// Migrate reads a YAML config file at path, applies the supplied migrations
// in order until the file's schema_version reaches or exceeds the highest
// migration's To value, and (optionally) writes the migrated YAML back.
//
// A file that already targets the latest version is a no-op; a file with no
// schema_version key is treated as version 0.
//
// The migrated raw map is returned regardless of WriteBack so callers can
// pass it on to other layers (or unmarshal into a typed config themselves).
func Migrate(path string, migrations []Migration, opts MigrateOptions) (map[string]any, error) {
	if len(migrations) == 0 {
		// Still parse so callers get a consistent shape; cheap.
		return readYAMLMap(path)
	}
	raw, err := readYAMLMap(path)
	if err != nil {
		return nil, err
	}
	migrated, applied, err := applyMigrations(raw, migrations, opts)
	if err != nil {
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	if opts.OnMigration != nil {
		for _, m := range applied {
			opts.OnMigration(path, m)
		}
	}
	if opts.WriteBack && len(applied) > 0 {
		if err := writeYAMLMap(path, migrated); err != nil {
			return nil, fmt.Errorf("migrate %s: write-back: %w", path, err)
		}
	}
	return migrated, nil
}

// applyMigrations runs migrations in order while the current version is below
// the highest To in the chain. Each migration must match the current version
// exactly (m.From == cur); chains with gaps will halt and return an error so
// the caller knows to fill in the missing step.
func applyMigrations(raw map[string]any, migrations []Migration, opts MigrateOptions) (map[string]any, []Migration, error) {
	if raw == nil {
		raw = map[string]any{}
	}
	key := schemaKeyOrDefault(opts.SchemaKey)
	cur, _ := raw[key].(int)
	if cur == 0 {
		// yaml.v3 unmarshals untyped ints to int; guard against the
		// less-common int64/float64 cases.
		if v, ok := raw[key]; ok {
			cur = toInt(v)
		}
	}
	// Sort migrations by From so out-of-order input still works.
	sorted := append([]Migration(nil), migrations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].From < sorted[j].From })

	highest := sorted[len(sorted)-1].To

	var applied []Migration
	for cur < highest {
		next := findMigration(sorted, cur)
		if next == nil {
			return nil, applied, fmt.Errorf(
				"no migration from version %d (highest target is %d); chain has a gap",
				cur, highest,
			)
		}
		out, err := next.Apply(raw)
		if err != nil {
			return nil, applied, fmt.Errorf(
				"migration %d -> %d: %w", next.From, next.To, err,
			)
		}
		if out == nil {
			return nil, applied, fmt.Errorf(
				"migration %d -> %d returned nil map", next.From, next.To,
			)
		}
		raw = out
		raw[key] = next.To
		cur = next.To
		applied = append(applied, *next)
	}
	return raw, applied, nil
}

func findMigration(sorted []Migration, from int) *Migration {
	for i := range sorted {
		if sorted[i].From == from {
			return &sorted[i]
		}
	}
	return nil
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	case float32:
		return int(t)
	}
	return 0
}

func readYAMLMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	return raw, nil
}

func writeYAMLMap(path string, raw map[string]any) error {
	data, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	// Preserve the file's existing mode if possible.
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, data, mode)
}
