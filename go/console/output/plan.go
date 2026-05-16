package output

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"gopkg.in/yaml.v3"
)

// RenderPlan writes a cli.Plan to w using the same format dispatch as
// Render. JSON and YAML serialize the Plan directly (preserving every
// field per the JSON tags); table format prints a header block of the
// top-level fields followed by a tab-aligned row per Effect.
//
// p is typed as any to avoid importing kit/console/cli (which would
// introduce a circular dependency: cli already imports output). The
// implementation type-asserts via the duck-typed planLike interface
// so any struct shaped like cli.Plan — same field set, same JSON tags
// — works without binding to the cli package.
//
// Format dispatches on the Format constants (Table, JSON, YAML).
// Unknown formats return an error with the same wording as Render.
func RenderPlan(w io.Writer, format string, p any) error {
	switch format {
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	case YAML:
		return yaml.NewEncoder(w).Encode(p)
	case Table:
		return renderPlanTable(w, p)
	}
	return fmt.Errorf("unknown output format %q (valid: json, table, yaml)", format)
}

// renderPlanTable prints the plan as a header block followed by an
// effects table. Header lines: COMMAND, GENERATED. Effects render
// through the existing renderTable path so column-priority + width
// fitting match every other table output.
func renderPlanTable(w io.Writer, p any) error {
	rv := reflect.ValueOf(p)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return fmt.Errorf("RenderPlan: nil plan")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("RenderPlan: expected struct, got %s", rv.Kind())
	}

	// Header: COMMAND <command>\nGENERATED <timestamp>\n.
	cmdField := rv.FieldByName("Command")
	if cmdField.IsValid() {
		fmt.Fprintf(w, "COMMAND   %s\n", cmdField.Interface())
	}
	if gen := rv.FieldByName("GeneratedAt"); gen.IsValid() {
		fmt.Fprintf(w, "GENERATED %v\n", gen.Interface())
	}

	// Warnings (if any) before the effects table.
	if warn := rv.FieldByName("Warnings"); warn.IsValid() && warn.Kind() == reflect.Slice && warn.Len() > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "WARNINGS")
		for i := 0; i < warn.Len(); i++ {
			fmt.Fprintf(w, "  %v\n", warn.Index(i).Interface())
		}
	}

	// Effects table — re-use the column-aware renderTable.
	effectsField := rv.FieldByName("Effects")
	if !effectsField.IsValid() || effectsField.Kind() != reflect.Slice {
		return nil
	}
	if effectsField.Len() == 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "(no effects)")
		return nil
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "EFFECTS")
	// renderTable receives the slice value directly via reflect.Interface().
	return renderTable(w, effectsField.Interface(), nil)
}
