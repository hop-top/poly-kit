package output

import (
	"fmt"
	"reflect"
	"strings"
)

// filterColumns returns the subset of cols whose header matches one of
// selected, in SELECTED order — --cols reorders as well as selects, so the
// user's order wins over struct field order. Repeated names emit once, at
// their first position. Unknown header names in selected produce an error
// listing the available headers in struct field order.
func filterColumns(cols []column, selected []string) ([]column, error) {
	byHeader := make(map[string]column, len(cols))
	for _, c := range cols {
		byHeader[c.header] = c
	}

	out := make([]column, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, name := range selected {
		c, ok := byHeader[name]
		if !ok {
			available := make([]string, 0, len(cols))
			for _, ac := range cols {
				available = append(available, ac.header)
			}
			return nil, fmt.Errorf("unknown column %q (valid: %s)",
				name, strings.Join(available, ", "))
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, c)
	}
	return out, nil
}

// projectToMaps converts data (a struct or slice of structs) into a slice
// of map[string]any keyed by `table:""` tag header, in struct field order.
// All tagged columns are included; callers wanting a --cols projection use
// projectToOrdered instead.
//
// Non-struct inputs are returned as-is to the caller. Used by the template
// path, where `.Items` must be key-indexable, so a plain map is required
// and key order is irrelevant.
func projectToMaps(data any) any {
	rv := reflect.ValueOf(data)

	switch rv.Kind() {
	case reflect.Slice:
		out := make([]map[string]any, rv.Len())
		for i := range rv.Len() {
			e := rv.Index(i)
			if e.Kind() == reflect.Ptr {
				e = e.Elem()
			}
			out[i] = structToMap(e)
		}
		return out
	case reflect.Ptr:
		if rv.IsNil() {
			return data
		}
		return structToMap(rv.Elem())
	case reflect.Struct:
		return structToMap(rv)
	default:
		return data
	}
}

// projectToOrdered is projectToMaps' order-preserving twin: it projects
// data down to the `table:""` headers named in cols, emitting them in COLS
// order rather than struct field order.
//
// A plain map[string]any cannot carry that order — encoding/json and
// yaml.v3 both sort map keys — so the result uses orderedMap, which
// implements MarshalJSON and MarshalYAML to emit pairs in slice order.
// This is what lets json/yaml honor --cols precedence (user order wins)
// the same way table/csv/text do via filterColumns.
//
// Non-struct inputs are returned as-is to the caller.
func projectToOrdered(data any, cols []string) any {
	rv := reflect.ValueOf(data)

	switch rv.Kind() {
	case reflect.Slice:
		out := make([]orderedMap, rv.Len())
		for i := range rv.Len() {
			e := rv.Index(i)
			if e.Kind() == reflect.Ptr {
				e = e.Elem()
			}
			out[i] = structToOrdered(e, cols)
		}
		return out
	case reflect.Ptr:
		if rv.IsNil() {
			return data
		}
		return structToOrdered(rv.Elem(), cols)
	case reflect.Struct:
		return structToOrdered(rv, cols)
	default:
		return data
	}
}

// TableHeaders returns the `table:""` tag headers declared on t in struct
// field order. Used by Dispatch to validate --cols up front and by template
// rendering to expose the full header list as `.Cols`.
//
// t may be a struct, a slice/array of structs, or a pointer to either; the
// function unwraps to the underlying struct type before scanning fields.
// Fields with no `table` tag (or `table:"-"`) are skipped. Returns nil when
// t does not resolve to a struct.
func TableHeaders(t reflect.Type) []string {
	for t != nil {
		switch t.Kind() {
		case reflect.Ptr, reflect.Slice, reflect.Array:
			t = t.Elem()
		default:
			goto done
		}
	}
done:
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("table")
		if tag == "" || tag == "-" {
			continue
		}
		header, _ := parseTableTag(tag)
		out = append(out, header)
	}
	return out
}

// structToMap collects every `table:""` tagged field of v into a map keyed
// by header. Key order is not meaningful — see structToOrdered when it is.
func structToMap(v reflect.Value) map[string]any {
	t := v.Type()
	out := make(map[string]any)
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("table")
		if tag == "" || tag == "-" {
			continue
		}
		header, _ := parseTableTag(tag)
		out[header] = v.Field(i).Interface()
	}
	return out
}

// structToOrdered collects the `table:""` tagged fields of v named in cols,
// in cols order. Repeated names emit once, at their first position. Names
// with no matching tagged field are skipped — Dispatch validates --cols up
// front via validateCols, so unknown names never reach here in practice.
func structToOrdered(v reflect.Value, cols []string) orderedMap {
	t := v.Type()
	byHeader := make(map[string]reflect.Value, t.NumField())
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("table")
		if tag == "" || tag == "-" {
			continue
		}
		header, _ := parseTableTag(tag)
		byHeader[header] = v.Field(i)
	}

	out := make(orderedMap, 0, len(cols))
	seen := make(map[string]struct{}, len(cols))
	for _, name := range cols {
		fv, ok := byHeader[name]
		if !ok {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, orderedEntry{Key: name, Value: fv.Interface()})
	}
	return out
}
