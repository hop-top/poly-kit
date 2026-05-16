package provenance

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"hop.top/kit/go/console/output"
)

// Verify walks v finding every Synthesized[T] / Cached[T] wrapper and
// confirms (a) IsSet is true, and (b) the Tracker on ctx has a
// matching Provenance entry at the wrapper's JSON-pointer path. Path
// resolution uses reflect to walk v; struct field JSON tags decide
// the path segment.
//
// Returns nil on success; *output.Error{Code: "PROVENANCE_MISSING",
// ExitCode: 6} on the aggregated violations.
//
// Verify is pure — no I/O, just reflect over v and consult the
// Tracker. Safe to call from tests; AssertProvenanceComplete is the
// blessed test-helper wrapper.
func Verify(ctx context.Context, v any) error {
	tr := Track(ctx)
	missing, invalid := collectViolations(v, tr)
	if len(missing) == 0 && len(invalid) == 0 {
		return nil
	}
	return buildError(missing, invalid)
}

// VerifyWith is the explicit-Tracker variant for callers (e.g., tests)
// that don't have a context to thread through. Otherwise identical to
// Verify.
func VerifyWith(t *Tracker, v any) error {
	if t == nil {
		t = NewTracker()
	}
	missing, invalid := collectViolations(v, t)
	if len(missing) == 0 && len(invalid) == 0 {
		return nil
	}
	return buildError(missing, invalid)
}

// collectViolations walks v and returns the sorted list of missing
// paths plus the sorted list of invalid paths (with their errors).
func collectViolations(v any, tr *Tracker) (missing []string, invalid []pathErr) {
	if v == nil || tr == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(v)
	w := walker{tracker: tr}
	w.walk(rv, "")
	sort.Strings(w.missing)
	sort.Slice(w.invalid, func(i, j int) bool { return w.invalid[i].path < w.invalid[j].path })
	return w.missing, w.invalid
}

type pathErr struct {
	path string
	err  error
}

func buildError(missing []string, invalid []pathErr) error {
	parts := make([]string, 0, len(missing)+len(invalid))
	parts = append(parts, missing...)
	for _, ie := range invalid {
		parts = append(parts, fmt.Sprintf("%s (%v)", ie.path, ie.err))
	}
	return output.ProvenanceMissingError(strings.Join(parts, ", "))
}

// walker recurses over a value graph, accumulating wrapper-field
// JSON-pointer paths and checking each against the Tracker.
type walker struct {
	tracker *Tracker
	missing []string
	invalid []pathErr
}

func (w *walker) walk(rv reflect.Value, path string) {
	if !rv.IsValid() {
		return
	}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return
		}
		w.walk(rv.Elem(), path)
	case reflect.Struct:
		// Check whether this struct itself is a Cached / Synthesized.
		if checked := w.checkWrapper(rv, path); checked {
			return
		}
		w.walkStruct(rv, path)
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			w.walk(rv.Index(i), path+"/"+strconv.Itoa(i))
		}
	case reflect.Map:
		// JSON serialization disallows non-string map keys; we walk
		// only string-keyed maps (best-effort).
		if rv.Type().Key().Kind() != reflect.String {
			return
		}
		iter := rv.MapRange()
		for iter.Next() {
			k := iter.Key().String()
			w.walk(iter.Value(), path+"/"+escapeJSONPointerToken(k))
		}
	}
}

// checkWrapper inspects rv for the wrapper-type hallmark: a struct
// with fields (value, prov, set) where prov is Provenance and set is
// bool. Returns true if rv is a wrapper (so the caller doesn't recurse
// into its inner fields and double-count).
func (w *walker) checkWrapper(rv reflect.Value, path string) bool {
	t := rv.Type()
	// Heuristic: the wrapper is a 3-field struct whose middle field is
	// `Provenance` and last is `bool`. We don't match by name (private
	// fields can't be read via reflect without unsafe), so we rely on
	// the type identity of the Provenance field. We use a method-based
	// detection: Cached[T] / Synthesized[T] expose a Provenance()
	// method returning Provenance, plus IsSet() returning bool.
	if !isWrapperType(t) {
		return false
	}
	provMethod := rv.MethodByName("Provenance")
	isSetMethod := rv.MethodByName("IsSet")
	if !provMethod.IsValid() || !isSetMethod.IsValid() {
		return false
	}
	provOut := provMethod.Call(nil)
	setOut := isSetMethod.Call(nil)
	if len(provOut) != 1 || len(setOut) != 1 {
		return false
	}
	prov, ok := provOut[0].Interface().(Provenance)
	if !ok {
		return false
	}
	isSet, ok := setOut[0].Interface().(bool)
	if !ok {
		return false
	}
	// If the wrapper was populated via a constructor, its own
	// Provenance is authoritative — record it as a "match" for the
	// tracker path automatically.
	if isSet && !prov.IsZero() {
		// Optionally cross-check against tracker if one is recorded.
		if tp, found := w.tracker.Lookup(path); found {
			// Both wrapper and tracker have a record. If they disagree
			// on URL+Version (the identity bits), surface it.
			if tp.URL != prov.URL && tp.URL != "" && prov.URL != "" {
				w.invalid = append(w.invalid, pathErr{path: path, err: fmt.Errorf(
					"tracker URL %q != wrapper URL %q", tp.URL, prov.URL)})
			}
		}
		return true
	}
	// Wrapper unpopulated — look up the tracker.
	if tp, found := w.tracker.Lookup(path); found && !tp.IsZero() {
		// Tracker carries it; ok.
		return true
	}
	// Check invalid map too: caller might have recorded an invalid
	// provenance which would still indicate "they tried, but it didn't
	// validate".
	if ierr, ok := w.tracker.invalidError(path); ok {
		w.invalid = append(w.invalid, pathErr{path: path, err: ierr})
		return true
	}
	w.missing = append(w.missing, path)
	return true
}

// isWrapperType reports whether t is a Cached[T] or Synthesized[T]
// from this package. We compare type names because generic-type
// identity through reflect is awkward.
func isWrapperType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	if t.PkgPath() != "hop.top/kit/go/runtime/provenance" {
		return false
	}
	name := t.Name()
	// generic instantiations show up as "Cached[whatever]" or "Synthesized[whatever]"
	return strings.HasPrefix(name, "Cached[") || strings.HasPrefix(name, "Synthesized[")
}

// walkStruct iterates the struct fields, building the JSON-pointer
// path segment from the json tag (or field name when no tag).
func (w *walker) walkStruct(rv reflect.Value, path string) {
	t := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		ft := t.Field(i)
		if !ft.IsExported() {
			continue
		}
		seg, omit := jsonFieldName(ft)
		if omit {
			continue
		}
		w.walk(rv.Field(i), path+"/"+escapeJSONPointerToken(seg))
	}
}

// jsonFieldName returns the JSON pointer token for sf and whether the
// field is omitted from JSON entirely (json:"-").
func jsonFieldName(sf reflect.StructField) (string, bool) {
	tag := sf.Tag.Get("json")
	if tag == "-" {
		return "", true
	}
	if tag == "" {
		return sf.Name, false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return sf.Name, false
	}
	return name, false
}

// escapeJSONPointerToken applies the RFC 6901 escape rules: "~" -> "~0",
// "/" -> "~1".
func escapeJSONPointerToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}
