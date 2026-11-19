package prompt

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Pattern is a named system prompt, stored as `<name>/system.md`.
type Pattern struct {
	Name   string
	System string
}

// PatternSource loads patterns by name. A missing pattern matches
// ErrNotFound.
type PatternSource interface {
	Load(name string) (*Pattern, error)
	// List returns the sorted names of every pattern, each once.
	List() ([]string, error)
}

// DirPatterns reads patterns from each dir in order; a name found in an
// earlier dir shadows the same name in a later one. A dir that does not
// exist is skipped.
func DirPatterns(dirs ...string) PatternSource { return dirPatterns(dirs) }

type dirPatterns []string

func (d dirPatterns) Load(name string) (*Pattern, error) {
	name, err := checkName("pattern", name)
	if err != nil {
		return nil, err
	}
	for _, dir := range d {
		b, err := os.ReadFile(filepath.Join(dir, name, "system.md")) //nolint:gosec // G304: caller-injected dir, name checked
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read pattern %q: %w", name, err)
		}
		return &Pattern{Name: name, System: string(b)}, nil
	}
	return nil, fmt.Errorf("%w: pattern %q", ErrNotFound, name)
}

func (d dirPatterns) List() ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, dir := range d {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list patterns in %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() && !seen[e.Name()] {
				seen[e.Name()] = true
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
