// loader.go bridges the embedded default policy table and
// caller-supplied YAML overrides. Two entry points:
//
//   - LoadFromFile(path) — reads a YAML file from disk.
//   - LoadOrDefault(path) — reads + merges; empty path returns the
//     embedded default verbatim.
//
// Used by the MCP adapter's `--policy <file>` flag (ADR-0019 §4)
// and by adopters wiring policy directly through their own
// entrypoint. Precedence is fixed by Merge: overlay > default.

package policy

import (
	"fmt"
	"os"
)

// LoadFromFile reads a YAML policy table from path and returns it.
// Errors carry the path so log output points at the failing file.
func LoadFromFile(path string) (Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return Table{}, fmt.Errorf("policy: open %s: %w", path, err)
	}
	defer f.Close()
	return Load(f, path)
}

// LoadOrDefault returns the embedded default when path is empty,
// or the merged table (default + overlay from the file) when path
// is set. Overlay rules win on (side_effect, network) collisions
// per ADR-0019.
func LoadOrDefault(path string) (Table, error) {
	if path == "" {
		return Default(), nil
	}
	overlay, err := LoadFromFile(path)
	if err != nil {
		return Table{}, err
	}
	return Merge(Default(), overlay), nil
}
