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
	// dotted segments, case-folded unless CaseSensitive.
	//
	// A mapping key containing '.' in a project file is read as its dotted
	// path, as viper reads it: `"bos.url": x` is `bos: {url: x}` for every
	// rule here. Forbidden and SecretKey apply to that path. A dotted key
	// that overlaps a Narrow key (same path, an ancestor or a descendant)
	// is refused, since it cannot be narrowed in place; write it as a
	// nested mapping instead. Other dotted keys (such as a host name used
	// as a map key) pass.
	Forbidden []string
	// SecretKey, when non-nil, is matched against each segment of every key
	// in the project file (raw and case-folded); a match is forbidden like
	// a Forbidden key.
	SecretKey *regexp.Regexp
	// Narrow lists keys a project file may only narrow.
	Narrow []NarrowKey
	// CaseSensitive turns off case folding. By default every rule matches
	// the case-folded key path: each segment is lower-cased with
	// strings.ToLower on both sides, so "Owner_Profile" is forbidden by
	// "owner_profile" and "AI: {MODELS: {ALLOW: ...}}" is narrowed by
	// "ai.models.allow". Folding is the safe default because viper-based
	// tools lower-case keys on merge, so a differently-cased key reaches
	// them as the rule's key. It only ever refuses or narrows more: kit's
	// own struct decode (yaml.v3) matches tags case-sensitively, so a
	// mis-cased key never sets a struct field anyway. Set CaseSensitive only
	// when every consumer of the merged result matches keys exactly.
	CaseSensitive bool
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
	// Dotted is true when the key is a dotted mapping key that overlaps a
	// narrow-only key and so cannot be narrowed in place.
	Dotted bool
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
	} else if e.Dotted {
		why = "is a dotted key overlapping a narrow-only key; write it as a nested mapping"
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

func (r *ProjectRules) fold(s string) string {
	if r.CaseSensitive {
		return s
	}
	return strings.ToLower(s)
}

// segs splits a dotted key into folded segments.
func (r *ProjectRules) segs(key string) []string {
	return strings.Split(r.fold(key), ".")
}

func hasPrefix(path, prefix []string) bool {
	return len(prefix) <= len(path) && slices.Equal(path[:len(prefix)], prefix)
}

func (r *ProjectRules) checkForbidden(file string, doc any, userPath string) error {
	suggest := "move it to the user config"
	if userPath != "" {
		suggest = "move it to " + userPath
	}
	forbidden := make([][]string, len(r.Forbidden))
	for i, f := range r.Forbidden {
		forbidden[i] = r.segs(f)
	}
	narrow := make([][]string, len(r.Narrow))
	for i, nk := range r.Narrow {
		narrow[i] = r.segs(nk.Key)
	}
	var walk func(prefix string, path []string, dotted bool, v any) error
	walk = func(prefix string, path []string, dotted bool, v any) error {
		switch m := v.(type) {
		case []any:
			// Elements share the sequence's path: "a.b" covers a: [{b: x}].
			for _, e := range m {
				if err := walk(prefix, path, dotted, e); err != nil {
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
				own := r.segs(k)
				p := append(slices.Clone(path), own...)
				d := dotted || len(own) > 1
				for _, f := range forbidden {
					if hasPrefix(p, f) {
						return &ErrProjectForbidden{File: file, Key: full, Suggestion: suggest}
					}
				}
				if r.SecretKey != nil {
					for _, raw := range strings.Split(k, ".") {
						if r.SecretKey.MatchString(raw) || r.SecretKey.MatchString(r.fold(raw)) {
							return &ErrProjectForbidden{File: file, Key: full, Secret: true, Suggestion: suggest}
						}
					}
				}
				if d {
					for _, n := range narrow {
						if hasPrefix(p, n) || hasPrefix(n, p) {
							return &ErrProjectForbidden{File: file, Key: full, Dotted: true, Suggestion: suggest}
						}
					}
				}
				if err := walk(full, p, d, m[k]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk("", nil, false, doc)
}

func joinKey(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

// slot is one map entry located by a case-folded path.
type slot struct {
	parent map[string]any
	key    string
}

// locate returns every entry whose folded path is segs: case variants of a
// key ("AI" and "ai") are all returned, in key order.
func (r *ProjectRules) locate(m map[string]any, segs []string) []slot {
	keys := make([]string, 0, len(m))
	for k := range m {
		if r.fold(k) == segs[0] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []slot
	for _, k := range keys {
		if len(segs) == 1 {
			out = append(out, slot{m, k})
			continue
		}
		if next, ok := m[k].(map[string]any); ok {
			out = append(out, r.locate(next, segs[1:])...)
		}
	}
	return out
}

// narrow rewrites doc's narrow keys against the list dst holds now (every
// layer merged so far: defaults, system, user).
func (r *ProjectRules) narrow(dst any, file string, doc map[string]any) error {
	var lowerDoc map[string]any
	lowerList := func(nk NarrowKey) ([]string, error) {
		if lowerDoc == nil {
			data, err := yaml.Marshal(dst)
			if err != nil {
				return nil, fmt.Errorf("config %s: read merged layers: %w", file, err)
			}
			if err := yaml.Unmarshal(data, &lowerDoc); err != nil {
				return nil, fmt.Errorf("config %s: read merged layers: %w", file, err)
			}
			if lowerDoc == nil {
				lowerDoc = map[string]any{}
			}
		}
		// Case variants in the lower layers: first in key order stands.
		ls := r.locate(lowerDoc, r.segs(nk.Key))
		if len(ls) == 0 || ls[0].parent[ls[0].key] == nil {
			return nil, nil
		}
		lower, err := scalarList(ls[0].parent[ls[0].key])
		if err != nil {
			return nil, fmt.Errorf("config %s: key %q in lower layers: %w", file, nk.Key, err)
		}
		return lower, nil
	}
	for _, nk := range r.Narrow {
		if nk.Mode != Union && nk.Mode != Intersect {
			return fmt.Errorf("config %s: narrow key %q: invalid mode %d", file, nk.Key, nk.Mode)
		}
		// Every case variant of the key in the project file is narrowed.
		for _, sl := range r.locate(doc, r.segs(nk.Key)) {
			raw := sl.parent[sl.key]
			if raw == nil {
				// A null would clear the lower list, widening it: keep lower.
				delete(sl.parent, sl.key)
				continue
			}
			project, err := scalarList(raw)
			if err != nil {
				return fmt.Errorf("config %s: key %q: %w", file, nk.Key, err)
			}
			lower, err := lowerList(nk)
			if err != nil {
				return err
			}
			result := narrowList(nk, lower, project)
			if nk.Mode == Intersect && len(result) == 0 {
				return &ErrNarrowEmpty{File: file, Key: nk.Key, Lower: lower, Project: project}
			}
			sl.parent[sl.key] = result
			if r.OnNarrow != nil {
				r.OnNarrow(Narrowed{File: file, Key: nk.Key, Mode: nk.Mode,
					Lower: lower, Project: project, Result: result})
			}
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
