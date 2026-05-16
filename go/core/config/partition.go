package config

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// reloadTagName is the struct-tag key consulted by Partition. A field
// tagged `reload:"true"` is mutable across hot-reloads; absence (the
// default) means immutable.
//
// The default-immutable bias is intentional: hot-reloading an unknown
// field surface is more dangerous than refusing to reload one. New
// fields opt in deliberately by tagging.
const reloadTagName = "reload"

// reloadTagValue is the only accepted truthy value for the reload tag.
// Anything else (including empty) means immutable. We refuse to coerce
// "1", "yes", or empty strings to keep the contract crisp.
const reloadTagValue = "true"

// partitionResult caches the (mutable, immutable) split per reflect.Type.
// Computing it once per type keeps Reload allocation-light on repeat calls
// against the same wrapper.
type partitionResult struct {
	mutable   []string
	immutable []string
	err       error
}

var partitionCache sync.Map // map[reflect.Type]*partitionResult

// Partition walks v (a non-nil pointer to a struct) and returns two
// dotted-path slices: mutable (fields tagged `reload:"true"`) and
// immutable (everything else). Embedded structs traverse recursively;
// their leaf paths use the parent yaml tag (or lower-cased field name)
// joined by dots.
//
// The leaf-vs-branch decision: a struct field tagged `reload:"true"`
// short-circuits — every nested field beneath it is mutable, no further
// walk distinction needed. An untagged struct field continues recursion
// so individual leaves can opt in independently.
//
// Maps and slices are treated as leaves: their path appears in exactly
// one bucket (governed by the field tag), and the value as a whole is
// compared with reflect.DeepEqual at diff time. Per-element opt-in is
// out of scope.
//
// Results are cached per reflect.Type. Calling Partition repeatedly
// against the same T is cheap.
//
// Returns an error if v is nil or not a pointer to a struct.
func Partition[T any](v *T) (mutable, immutable []string, err error) {
	if v == nil {
		return nil, nil, fmt.Errorf("config.Partition: v is nil")
	}
	rv := reflect.ValueOf(v).Elem()
	rt := rv.Type()
	if rt.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf(
			"config.Partition: expected pointer to struct, got %s", rt.Kind(),
		)
	}
	if cached, ok := partitionCache.Load(rt); ok {
		r := cached.(*partitionResult)
		return cloneStrings(r.mutable), cloneStrings(r.immutable), r.err
	}
	mut, imm, perr := partitionType(rt, "", false)
	sort.Strings(mut)
	sort.Strings(imm)
	res := &partitionResult{mutable: mut, immutable: imm, err: perr}
	partitionCache.Store(rt, res)
	return cloneStrings(mut), cloneStrings(imm), perr
}

// partitionType recursively walks rt and assigns each leaf path to either
// the mutable or immutable bucket. parentMutable propagates a `reload:"true"`
// tag from an enclosing struct field, so every leaf below an opted-in
// branch inherits mutability.
func partitionType(rt reflect.Type, prefix string, parentMutable bool) (mutable, immutable []string, err error) {
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}
		path := fieldPath(prefix, field)
		mutableHere := parentMutable || isReloadTagged(field)

		// Embedded struct: recurse. Anonymous embeds with no yaml tag
		// inline at the parent level (matching yaml's "inline" behavior),
		// otherwise the embed's name becomes a path segment.
		ft := field.Type
		if ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && !isLeafStruct(ft) {
			subPrefix := path
			if field.Anonymous && field.Tag.Get("yaml") == "" {
				subPrefix = prefix
			}
			subMut, subImm, subErr := partitionType(ft, subPrefix, mutableHere)
			if subErr != nil {
				return nil, nil, subErr
			}
			mutable = append(mutable, subMut...)
			immutable = append(immutable, subImm...)
			continue
		}

		if mutableHere {
			mutable = append(mutable, path)
		} else {
			immutable = append(immutable, path)
		}
	}
	return mutable, immutable, nil
}

// isReloadTagged reports whether the field carries `reload:"true"`.
// Any other value (including empty) means immutable.
func isReloadTagged(f reflect.StructField) bool {
	return f.Tag.Get(reloadTagName) == reloadTagValue
}

// fieldPath joins prefix with the field's yaml-tag-derived name (or
// lower-cased field name) using a dot separator.
func fieldPath(prefix string, f reflect.StructField) string {
	name := yamlName(f)
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// yamlName returns the yaml tag's first segment (stripping ",omitempty"
// and similar) or the lower-cased field name when no tag is set.
func yamlName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	if tag == "" || tag == "-" {
		return lowerASCII(f.Name)
	}
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			tag = tag[:i]
			break
		}
	}
	if tag == "" {
		return lowerASCII(f.Name)
	}
	return tag
}

func lowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// isLeafStruct reports whether a struct type should be treated as an
// opaque leaf rather than a branch to recurse into. The list is small
// and conservative: when in doubt, recurse.
func isLeafStruct(rt reflect.Type) bool {
	pkg := rt.PkgPath()
	name := rt.Name()
	if pkg == "time" && name == "Time" {
		return true
	}
	return false
}

// FieldDiff carries the before/after values for a single dotted path. It
// is the value type of the diff map produced by diffPaths and surfaced in
// reload bus events.
type FieldDiff struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// diffPaths compares old and fresh at each path in paths and returns
// only the entries whose values differ (per reflect.DeepEqual).
//
// old and fresh must be non-nil pointers of the same struct type.
//
// Paths are dotted strings produced by Partition. Each lookup uses the
// yaml-tag-derived name to traverse the struct, which means callers can
// stay agnostic to Go field renames as long as the yaml tag remains stable.
func diffPaths[T any](old, fresh *T, paths []string) (map[string]FieldDiff, error) {
	if old == nil || fresh == nil {
		return nil, fmt.Errorf("config.diffPaths: nil snapshot")
	}
	out := map[string]FieldDiff{}
	oldRV := reflect.ValueOf(old).Elem()
	freshRV := reflect.ValueOf(fresh).Elem()
	for _, p := range paths {
		oldVal, oldOK := lookupPath(oldRV, p)
		newVal, newOK := lookupPath(freshRV, p)
		if !oldOK || !newOK {
			// A path that doesn't resolve in either side is a Partition
			// bug, not a user error; surface it loudly.
			return nil, fmt.Errorf(
				"config.diffPaths: path %q not resolvable (old=%v new=%v)",
				p, oldOK, newOK,
			)
		}
		if !reflect.DeepEqual(oldVal, newVal) {
			out[p] = FieldDiff{From: oldVal, To: newVal}
		}
	}
	return out, nil
}

// lookupPath walks rv following dotted yaml-tag-derived names and returns
// the resolved value as an interface{}. Traversal handles pointer
// indirection (treating nil pointers as zero values).
func lookupPath(rv reflect.Value, path string) (any, bool) {
	if path == "" {
		return rv.Interface(), true
	}
	cur := rv
	for len(path) > 0 {
		if cur.Kind() == reflect.Pointer {
			if cur.IsNil() {
				return nil, true
			}
			cur = cur.Elem()
		}
		if cur.Kind() != reflect.Struct {
			return nil, false
		}
		seg := path
		dot := -1
		for i := 0; i < len(path); i++ {
			if path[i] == '.' {
				dot = i
				break
			}
		}
		if dot >= 0 {
			seg = path[:dot]
			path = path[dot+1:]
		} else {
			path = ""
		}
		cur = findField(cur, seg)
		if !cur.IsValid() {
			return nil, false
		}
	}
	if cur.Kind() == reflect.Pointer && cur.IsNil() {
		return nil, true
	}
	return cur.Interface(), true
}

// findField returns the struct field whose yaml-tag-derived name matches
// seg, including anonymous embedded structs (whose own fields are
// inlined per yaml conventions). Returns the zero Value when no match.
func findField(rv reflect.Value, seg string) reflect.Value {
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if f.Anonymous && f.Tag.Get("yaml") == "" && ft.Kind() == reflect.Struct && !isLeafStruct(ft) {
			fieldVal := rv.Field(i)
			if fieldVal.Kind() == reflect.Pointer {
				if fieldVal.IsNil() {
					continue
				}
				fieldVal = fieldVal.Elem()
			}
			if got := findField(fieldVal, seg); got.IsValid() {
				return got
			}
			continue
		}
		if yamlName(f) == seg {
			return rv.Field(i)
		}
	}
	return reflect.Value{}
}

// cloneStrings returns a defensive copy of in. Partition results are
// cached per type, so handing callers the cached slice would let them
// mutate it and corrupt later lookups.
func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
