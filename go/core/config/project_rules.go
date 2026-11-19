package config

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// NarrowMode says how a project-layer list combines with the list the
// layers below it (defaults, system, user) produced.
type NarrowMode int

const (
	// Union is deny-style: the project's entries are added to the lower
	// list. The project can only forbid more.
	Union NarrowMode = iota + 1
	// Intersect is allow-style: only entries both lists admit survive. The
	// project can only permit less. An empty result is [ErrNarrowEmpty].
	Intersect
)

// NarrowKey declares one list key a project file may only narrow.
type NarrowKey struct {
	// Key is the dotted YAML key of a list of scalars.
	Key string
	// Mode is [Union] or [Intersect].
	Mode NarrowMode
	// Match reports whether pattern admits value; nil means exact equality.
	// Use it for glob or URI matching. Intersect keeps a project entry when
	// some lower entry admits it as a value, and a lower entry when some
	// project entry admits it, so the result never permits more than the
	// lower list did.
	Match func(pattern, value string) bool
}

// ProjectRules restricts what the project layer (Options.ProjectConfigPath)
// may do. It never applies to system, user, extra (-c) files, env or
// overrides.
type ProjectRules struct {
	// Forbidden keys a project file may not set: exact dotted keys or
	// prefixes ("bos" forbids "bos" and every "bos.*"). Matching is on whole
	// dotted segments.
	Forbidden []string
	// SecretKey, when non-nil, is matched against each segment of every key
	// in the project file; a match is forbidden like a Forbidden key.
	SecretKey *regexp.Regexp
	// Narrow lists keys a project file may only narrow.
	Narrow []NarrowKey
	// OnNarrow, when non-nil, is called once per narrowed key present in
	// the project file, after the result is computed.
	OnNarrow func(Narrowed)
}

// Narrowed reports one narrowed key: what the lower layers held, what the
// project file asked for, and what replaced both.
type Narrowed struct {
	File    string
	Key     string
	Mode    NarrowMode
	Lower   []string
	Project []string
	Result  []string
}

// ErrProjectForbidden is returned by Load when the project file sets a key
// [ProjectRules] forbids.
type ErrProjectForbidden struct {
	File string
	Key  string
	// Secret is true when SecretKey matched rather than Forbidden.
	Secret bool
	// NonString is true when the key is not a plain string (for example
	// `1:` or `? [a]`): rules cannot judge it, so a project file may not
	// hold one.
	NonString bool
	// Suggestion says where the key belongs.
	Suggestion string
}

func (e *ErrProjectForbidden) Error() string {
	why := "may not be set in a project config"
	if e.NonString {
		why = "is not a string key; project configs may only use string keys"
	} else if e.Secret {
		why = "looks like a secret and may not be set in a project config"
	}
	return fmt.Sprintf("config %s: key %q %s; %s", e.File, e.Key, why, e.Suggestion)
}

// ErrNarrowEmpty is returned by Load when an [Intersect] key leaves no
// entries. An empty allow list would usually read as "no limit", so Load
// refuses rather than widen; a tool that wants "deny everything" can map
// this error to that outcome.
type ErrNarrowEmpty struct {
	File    string
	Key     string
	Lower   []string
	Project []string
}

func (e *ErrNarrowEmpty) Error() string {
	return fmt.Sprintf(
		"config %s: key %q admits nothing: project list %v leaves no entry of %v",
		e.File, e.Key, e.Project, e.Lower)
}

// mergeProjectFile is mergeFileWithMigrations for the project slot with
// rules applied: forbidden keys are refused, narrow keys are replaced by
// their narrowed value, then the document merges into dst as usual.
func mergeProjectFile(dst any, path string, opts Options) error {
	data, err := fileData(path, opts)
	if err != nil {
		return err
	}
	// Decode into any so a non-string key anywhere (the root included)
	// surfaces as map[any]any instead of being coerced.
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == nil {
		return yaml.Unmarshal(data, dst)
	}
	rules := opts.Project
	if err := rules.checkForbidden(path, raw, opts.UserConfigPath); err != nil {
		return err
	}
	doc, ok := raw.(map[string]any)
	if !ok {
		// Not a mapping: let dst's decode report the kind error.
		return yaml.Unmarshal(data, dst)
	}
	if err := rules.narrow(dst, path, doc); err != nil {
		return err
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("re-marshal %s: %w", path, err)
	}
	return yaml.Unmarshal(out, dst)
}

func (r *ProjectRules) checkForbidden(file string, doc any, userPath string) error {
	suggest := "move it to the user config"
	if userPath != "" {
		suggest = "move it to " + userPath
	}
	var walk func(prefix string, v any) error
	walk = func(prefix string, v any) error {
		switch m := v.(type) {
		case []any:
			// Elements share the sequence's path: "a.b" covers a: [{b: x}].
			for _, e := range m {
				if err := walk(prefix, e); err != nil {
					return err
				}
			}
		case map[any]any:
			// yaml.v3 yields this for any non-string key. Fail closed.
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, fmt.Sprint(k))
			}
			sort.Strings(keys)
			return &ErrProjectForbidden{File: file, Key: joinKey(prefix, keys[0]),
				NonString: true, Suggestion: suggest}
		case map[string]any:
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				full := joinKey(prefix, k)
				for _, f := range r.Forbidden {
					if full == f {
						return &ErrProjectForbidden{File: file, Key: full, Suggestion: suggest}
					}
				}
				if r.SecretKey != nil && r.SecretKey.MatchString(k) {
					return &ErrProjectForbidden{File: file, Key: full, Secret: true, Suggestion: suggest}
				}
				if err := walk(full, m[k]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk("", doc)
}

func joinKey(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

// narrow rewrites doc's narrow keys against the list dst holds now (every
// layer merged so far: defaults, system, user).
func (r *ProjectRules) narrow(dst any, file string, doc map[string]any) error {
	var lowerDoc map[string]any
	for _, nk := range r.Narrow {
		if nk.Mode != Union && nk.Mode != Intersect {
			return fmt.Errorf("config %s: narrow key %q: invalid mode %d", file, nk.Key, nk.Mode)
		}
		raw, ok := getDotted(doc, nk.Key)
		if !ok {
			continue
		}
		if raw == nil {
			// A null would clear the lower list, widening it: keep lower.
			deleteDotted(doc, nk.Key)
			continue
		}
		project, err := scalarList(raw)
		if err != nil {
			return fmt.Errorf("config %s: key %q: %w", file, nk.Key, err)
		}
		if lowerDoc == nil {
			data, err := yaml.Marshal(dst)
			if err != nil {
				return fmt.Errorf("config %s: read merged layers: %w", file, err)
			}
			if err := yaml.Unmarshal(data, &lowerDoc); err != nil {
				return fmt.Errorf("config %s: read merged layers: %w", file, err)
			}
			if lowerDoc == nil {
				lowerDoc = map[string]any{}
			}
		}
		var lower []string
		if lv, ok := getDotted(lowerDoc, nk.Key); ok && lv != nil {
			if lower, err = scalarList(lv); err != nil {
				return fmt.Errorf("config %s: key %q in lower layers: %w", file, nk.Key, err)
			}
		}
		result := narrowList(nk, lower, project)
		if nk.Mode == Intersect && len(result) == 0 {
			return &ErrNarrowEmpty{File: file, Key: nk.Key, Lower: lower, Project: project}
		}
		if err := setDottedKey(doc, nk.Key, result); err != nil {
			return fmt.Errorf("config %s: key %q: %w", file, nk.Key, err)
		}
		if r.OnNarrow != nil {
			r.OnNarrow(Narrowed{File: file, Key: nk.Key, Mode: nk.Mode,
				Lower: lower, Project: project, Result: result})
		}
	}
	return nil
}

// narrowList combines lower and project. No lower list: project stands.
func narrowList(nk NarrowKey, lower, project []string) []string {
	if len(lower) == 0 {
		return dedupe(project)
	}
	if nk.Mode == Union {
		return dedupe(append(slices.Clone(lower), project...))
	}
	match := nk.Match
	if match == nil {
		match = func(p, v string) bool { return p == v }
	}
	admits := func(patterns []string, v string) bool {
		return slices.ContainsFunc(patterns, func(p string) bool { return match(p, v) })
	}
	var out []string
	for _, p := range project {
		if admits(lower, p) {
			out = append(out, p)
		}
	}
	for _, l := range lower {
		if admits(project, l) {
			out = append(out, l)
		}
	}
	return dedupe(out)
}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func scalarList(v any) ([]string, error) {
	seq, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected a list of scalars, got %T", v)
	}
	out := make([]string, 0, len(seq))
	for _, e := range seq {
		switch e.(type) {
		case map[string]any, []any, nil:
			return nil, fmt.Errorf("expected a list of scalars, found %T entry", e)
		}
		out = append(out, fmt.Sprint(e))
	}
	return out, nil
}

func getDotted(m map[string]any, key string) (any, bool) {
	segs := strings.Split(key, ".")
	cur := m
	for _, s := range segs[:len(segs)-1] {
		next, ok := cur[s].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	v, ok := cur[segs[len(segs)-1]]
	return v, ok
}

func deleteDotted(m map[string]any, key string) {
	segs := strings.Split(key, ".")
	cur := m
	for _, s := range segs[:len(segs)-1] {
		next, ok := cur[s].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	delete(cur, segs[len(segs)-1])
}
