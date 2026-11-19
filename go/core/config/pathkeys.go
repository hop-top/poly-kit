package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Path-typed keys (Options.PathKeys): a relative value is made absolute
// against a base directory so the tool never depends on its own cwd.
//
//   - A file layer (system, user, project, extra) resolves against the
//     directory of the file that supplied the value, except a project file
//     named config.yaml inside a dot-directory (".tool/config.yaml"), which
//     resolves against the project root, the parent of that directory.
//     ".tool.yaml" and "tool.yaml" resolve against their own directory.
//   - EnvOverride and Overrides (-c key=value) resolve against the cwd.
//   - Absolute values and values starting with "~" are left as written
//     (expand "~" yourself); empty values stay empty.
//
// A key is dotted, matched case-folded per segment like [ProjectRules];
// "*" matches any one segment (map of paths). The value may be a string or a
// list of strings.

// resolveFilePaths rewrites path keys in a file's YAML. data is returned
// untouched when no key needs it.
func resolveFilePaths(data []byte, path string, opts Options) ([]byte, error) {
	if len(opts.PathKeys) == 0 {
		return data, nil
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return data, nil // the caller's decode reports it
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return data, nil
	}
	base, err := fileBase(path, path == opts.ProjectConfigPath)
	if err != nil {
		return nil, err
	}
	if !resolveDocPaths(m, opts.PathKeys, base) {
		return data, nil
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("re-marshal %s: %w", path, err)
	}
	return out, nil
}

// fileBase is the directory relative values in file resolve against.
func fileBase(file string, project bool) (string, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", file, err)
	}
	dir := filepath.Dir(abs)
	if project && filepath.Base(abs) == "config.yaml" &&
		strings.HasPrefix(filepath.Base(dir), ".") && len(filepath.Base(dir)) > 1 {
		return filepath.Dir(dir), nil
	}
	return dir, nil
}

// resolveDocPaths resolves every path key present in doc in place and
// reports whether it changed anything.
func resolveDocPaths(doc map[string]any, keys []string, base string) bool {
	changed := false
	for _, k := range keys {
		if k == "" {
			continue
		}
		if resolveAt(doc, strings.Split(strings.ToLower(k), "."), base) {
			changed = true
		}
	}
	return changed
}

func resolveAt(m map[string]any, segs []string, base string) bool {
	changed := false
	for k, v := range m {
		if segs[0] != "*" && strings.ToLower(k) != segs[0] {
			continue
		}
		if len(segs) == 1 {
			if nv, ch := resolveValue(v, base); ch {
				m[k] = nv
				changed = true
			}
			continue
		}
		switch c := v.(type) {
		case map[string]any:
			if resolveAt(c, segs[1:], base) {
				changed = true
			}
		case []any:
			for _, e := range c {
				if em, ok := e.(map[string]any); ok && resolveAt(em, segs[1:], base) {
					changed = true
				}
			}
		}
	}
	return changed
}

func resolveValue(v any, base string) (any, bool) {
	switch x := v.(type) {
	case string:
		r := resolvePath(x, base)
		return r, r != x
	case []any:
		changed := false
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = e
			if s, ok := e.(string); ok {
				if r := resolvePath(s, base); r != s {
					out[i] = r
					changed = true
				}
			}
		}
		return out, changed
	}
	return v, false
}

func resolvePath(s, base string) string {
	if s == "" || filepath.IsAbs(s) || strings.HasPrefix(s, "~") {
		return s
	}
	return filepath.Join(base, s)
}

// resolveOverridePaths returns overrides with path keys resolved against
// the cwd; the input is not modified.
func resolveOverridePaths(overrides map[string]any, keys []string) (map[string]any, error) {
	if len(overrides) == 0 || len(keys) == 0 {
		return overrides, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve relative paths: %w", err)
	}
	cp := deepCopyDoc(overrides).(map[string]any)
	resolveDocPaths(cp, keys, cwd)
	return cp, nil
}

func deepCopyDoc(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = deepCopyDoc(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = deepCopyDoc(e)
		}
		return out
	}
	return v
}

// resolveEnvPaths resolves path keys among the leaves EnvOverride changed.
// before/after are leafValues snapshots of dst around the callback.
func resolveEnvPaths(dst any, changed []string, keys []string) error {
	if len(keys) == 0 || len(changed) == 0 {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve relative paths: %w", err)
	}
	data, err := yaml.Marshal(dst)
	if err != nil {
		return err
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	resolved := deepCopyDoc(m).(map[string]any)
	if !resolveDocPaths(resolved, keys, cwd) {
		return nil
	}
	before, after := flattenDoc(m), flattenDoc(resolved)
	overlay := map[string]any{}
	for _, p := range changed {
		if fmt.Sprint(before[p]) != fmt.Sprint(after[p]) {
			if err := setDottedKey(overlay, p, after[p]); err != nil {
				return err
			}
		}
	}
	return ApplyOverrides(dst, overlay)
}
