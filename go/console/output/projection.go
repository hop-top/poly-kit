package output

import (
	"fmt"
	"reflect"
	"strings"
)

// filterColumns returns the subset of cols whose header matches one of
// selected. Order is preserved from cols (struct field order), not from
// selected. Unknown header names in selected produce an error listing the
// available headers.
func filterColumns(cols []column, selected []string) ([]column, error) {
	want := make(map[string]struct{}, len(selected))
	for _, name := range selected {
		want[name] = struct{}{}
	}

	out := make([]column, 0, len(want))
	have := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		have[c.header] = struct{}{}
		if _, keep := want[c.header]; keep {
			out = append(out, c)
		}
	}

	for name := range want {
		if _, ok := have[name]; !ok {
			available := make([]string, 0, len(cols))
			for _, c := range cols {
				available = append(available, c.header)
			}
			return nil, fmt.Errorf("unknown column %q (valid: %s)",
				name, strings.Join(available, ", "))
		}
	}
	return out, nil
}

// projectToMaps converts data (a struct or slice of structs) into a slice
// of map[string]any keyed by `table:""` tag header. When cols is non-empty,
// only matching columns are included; when empty, all tagged columns are
// included.
//
// This lets JSON/YAML formatters honor --cols by emitting only the
// projected fields instead of the full struct (which would also include
// fields without `table` tags via their `json`/`yaml` tags).
//
// Non-slice scalar values are returned as a single-element slice for
// uniformity. Non-struct inputs are returned as-is to caller.
func projectToMaps(data any, cols []string) any {
	rv := reflect.ValueOf(data)

	switch rv.Kind() {
	case reflect.Slice:
		out := make([]map[string]any, rv.Len())
		for i := range rv.Len() {
			e := rv.Index(i)
			if e.Kind() == reflect.Ptr {
				e = e.Elem()
			}
			out[i] = structToMap(e, cols)
		}
		return out
	case reflect.Ptr:
		if rv.IsNil() {
			return data
		}
		return structToMap(rv.Elem(), cols)
	case reflect.Struct:
		return structToMap(rv, cols)
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

func structToMap(v reflect.Value, cols []string) map[string]any {
	t := v.Type()
	want := make(map[string]struct{}, len(cols))
	for _, n := range cols {
		want[n] = struct{}{}
	}

	out := make(map[string]any)
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("table")
		if tag == "" || tag == "-" {
			continue
		}
		header, _ := parseTableTag(tag)
		if len(want) > 0 {
			if _, keep := want[header]; !keep {
				continue
			}
		}
		out[header] = v.Field(i).Interface()
	}
	return out
}
