package prompt

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Technique wraps a system prompt with prefix and suffix instructions that
// shape how the model reasons: chain of thought, step-back, and so on. It
// is a single-call prompt transform, not a multi-call strategy.
type Technique struct {
	Name        string
	Description string
	Prefix      string
	Suffix      string
}

// Builtins returns the shipped techniques: cot, tot, aot, reflexion and
// step-back. The slice is a fresh copy.
func Builtins() []Technique {
	return append([]Technique(nil), builtins...)
}

// Wrap assembles prefix, system and suffix joined by a blank line; empty
// parts are dropped.
func Wrap(t Technique, system string) string {
	var parts []string
	for _, p := range []string{t.Prefix, system, t.Suffix} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

// TechniqueSource loads techniques by name. A missing one matches
// ErrNotFound.
type TechniqueSource interface {
	Load(name string) (*Technique, error)
	// List returns every technique once, sorted by name.
	List() ([]Technique, error)
}

// DirTechniques reads `<name>.md` files from each dir in order, then falls
// back to Builtins: a file shadows a builtin of the same name, an earlier
// dir shadows a later one. A file is an optional `# description` first
// line, the prefix, and after a `---` line the suffix. A dir that does not
// exist is skipped.
func DirTechniques(dirs ...string) TechniqueSource { return dirTechniques(dirs) }

type dirTechniques []string

func (d dirTechniques) Load(name string) (*Technique, error) {
	name, err := checkName("technique", name)
	if err != nil {
		return nil, err
	}
	for _, dir := range d {
		t, err := readTechnique(filepath.Join(dir, name+".md"), name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return t, nil
	}
	for _, b := range builtins {
		if b.Name == name {
			return &b, nil
		}
	}
	return nil, fmt.Errorf("%w: technique %q", ErrNotFound, name)
}

func (d dirTechniques) List() ([]Technique, error) {
	seen := map[string]bool{}
	var out []Technique
	for _, dir := range d {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list techniques in %s: %w", dir, err)
		}
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".md")
			if e.IsDir() || !ok || seen[name] {
				continue
			}
			t, err := readTechnique(filepath.Join(dir, e.Name()), name)
			if err != nil {
				return nil, err
			}
			seen[name] = true
			out = append(out, *t)
		}
	}
	for _, b := range builtins {
		if !seen[b.Name] {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func readTechnique(path, name string) (*Technique, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: caller-injected dir, name checked
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err //nolint:wrapcheck // callers test for absence
		}
		return nil, fmt.Errorf("read technique %q: %w", name, err)
	}
	return parseTechnique(name, string(b)), nil
}

func parseTechnique(name, content string) *Technique {
	t := &Technique{Name: name}
	first, rest, _ := strings.Cut(content, "\n")
	if desc, ok := strings.CutPrefix(first, "# "); ok {
		t.Description = desc
		content = strings.TrimSpace(rest)
	}
	prefix, suffix, hasSuffix := strings.Cut(content, "\n---\n")
	t.Prefix = strings.TrimSpace(prefix)
	if hasSuffix {
		t.Suffix = strings.TrimSpace(suffix)
	}
	return t
}
