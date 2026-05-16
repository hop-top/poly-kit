// Package output renders structured values to an io.Writer in one of three
// formats: table, json, or yaml.
//
// The Format constants (Table, JSON, YAML) correspond to the values accepted
// by the --format flag defined in the cli package. Render should be called
// with the format value obtained from viper ("format" key) after the root
// command is constructed.
//
// Table rendering is driven by the `table` struct tag. Only fields with a
// non-empty, non-"-" table tag are included. The tag value becomes the column
// header. Fields without a table tag are silently omitted.
//
//	type Item struct {
//	    ID       string `table:"ID,priority=9"   json:"id"`
//	    Name     string `table:"Name,priority=8" json:"name"`
//	    Notes    string `table:"Notes,priority=2" json:"notes"`
//	    internal string  // no tag — not rendered
//	}
//
// The optional priority=N option (0-9, default 5) controls which columns are
// hidden first when total content width exceeds terminal width. Lower priority
// hides first; stable column order (no reordering, only hide).
//
// Render accepts both a single struct and a slice of structs for table mode.
// For JSON and YAML, v is passed directly to the respective encoder.
//
// Note: an empty slice produces no output at all — not even a header row.
// If callers need to show "no results" messaging, check len before calling Render.
package output

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Format is the output format specifier. Use the package-level constants
// Table, JSON, and YAML; do not construct arbitrary string values.
type Format = string

const (
	JSON  Format = "json"  // JSON renders v as indented JSON.
	YAML  Format = "yaml"  // YAML renders v as YAML.
	Table Format = "table" // Table renders v using struct `table:""` tags.
	CSV   Format = "csv"   // CSV renders v as comma-separated values.
	Text  Format = "text"  // Text renders v as plain text (kv/lines/paragraph).
	Human Format = "human" // Human renders v via a bespoke per-type renderer.
)

// defaultPriority is assigned to columns whose tag omits priority=N.
const defaultPriority = 5

// fallbackWidth is the width used when stdout is not a TTY (pipes, tests).
// Wide enough to fit most realistic tables without forcing column hides.
const fallbackWidth = 200

// columnSeparator is the inter-column padding used by the tabwriter and by
// width estimation. tabwriter.NewWriter passes 2 as padding below.
const columnSeparator = 2

// RegisterFlags adds the standard output persistent flags to cmd and binds
// them to keys in v. Equivalent to RegisterFlagsWith(cmd, v) with no
// options. The default --format value is "table".
//
// Registered flags: --format, --format-opt, --format-help, --cols (alias
// --columns), --template, --output (-o).
func RegisterFlags(cmd *cobra.Command, v *viper.Viper) {
	RegisterFlagsWith(cmd, v)
}

// stderrWriter is the destination for trailing provenance footers in
// Table mode. Tests redirect it; production code writes to os.Stderr.
var stderrWriter io.Writer = os.Stderr

// Render writes v to w in the requested format.
//
// Render is a thin compatibility shim over the Default registry. Resolution
// order: registry lookup by format key. Existing callers passing "json",
// "yaml", or "table" see no behavior change.
//
// Optional RenderOptions configure per-call behavior. WithProvenance(m)
// attaches a Metadata envelope: JSON/YAML wrap the payload in
// {"data": <v>, "_meta": <m>}; Table prints a single trailing stderr
// footer line and renders v unchanged.
//
// Adopters needing per-format options or column selection should call
// Default.Lookup(format) and invoke Formatter.Render directly with Options
// and cols.
func Render(w io.Writer, format Format, v any, opts ...RenderOption) error {
	f, ok := Default.Lookup(format)
	if !ok {
		return fmt.Errorf("unknown output format %q (valid: %s)",
			format, strings.Join(Default.Keys(), ", "))
	}

	cfg := renderConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.tableStyle == nil {
		cfg.tableStyle = getDefaultTableStyle()
	}

	payload := v
	if cfg.provenance != nil && format != Table {
		payload = struct {
			Data any       `json:"data"  yaml:"data"`
			Meta *Metadata `json:"_meta" yaml:"_meta"`
		}{Data: v, Meta: cfg.provenance}
	}

	// Styled table path: when the caller supplied WithTableStyle and the
	// writer is a TTY, route through the lipgloss-backed renderer instead
	// of the registered tabwriter formatter. Non-TTY writers (pipes,
	// files, tests) always fall through to the plain renderer so command
	// output stays diff-friendly and ANSI-free.
	if format == Table && cfg.tableStyle != nil && writerIsTTY(w) {
		if err := renderStyledTable(w, payload, nil, *cfg.tableStyle, cfg.rowEmphasis); err != nil {
			return err
		}
	} else {
		if err := f.Render(w, payload, nil, nil); err != nil {
			return err
		}
	}

	if cfg.provenance != nil && format == Table {
		fmt.Fprintf(stderrWriter, "Source: %s (fetched %s, method=%s)\n",
			cfg.provenance.Source,
			cfg.provenance.FetchedAt.Format(time.RFC3339),
			cfg.provenance.Method,
		)
	}
	return nil
}

// renderTable writes v as an aligned table. When v is a slice, all elements
// must be the same concrete struct type; column headers are derived once from
// the first element's `table` tags and reused for every row.
//
// selected, when non-empty, restricts output to columns whose tag header
// matches one of the names (preserving the order in which they appear in the
// struct, NOT the order in selected — column order is always struct order).
// An unknown name in selected returns an error.
func renderTable(w io.Writer, v any, selected []string) error {
	rv := reflect.ValueOf(v)

	var elemType reflect.Type
	var elems []reflect.Value
	if rv.Kind() == reflect.Slice {
		if rv.Len() == 0 {
			return nil
		}
		elemType = rv.Index(0).Type()
		elems = make([]reflect.Value, rv.Len())
		for i := range rv.Len() {
			e := rv.Index(i)
			if e.Kind() == reflect.Ptr {
				e = e.Elem()
			}
			elems[i] = e
		}
	} else {
		elemType = rv.Type()
		elems = []reflect.Value{rv}
	}

	cols := tableColumns(elemType)
	if len(cols) == 0 {
		return nil
	}
	if len(selected) > 0 {
		filtered, err := filterColumns(cols, selected)
		if err != nil {
			return err
		}
		// Re-number colIdx so it indexes the post-filter row layout used
		// by the renderers below. filterColumns preserves struct order
		// but does not renumber, so we do it here.
		for i := range filtered {
			filtered[i].colIdx = i
		}
		cols = filtered
	}

	rows := make([][]string, len(elems))
	for i, e := range elems {
		row := make([]string, len(cols))
		for j, c := range cols {
			row[j] = linkifyCell(fmt.Sprintf("%v", e.Field(c.fieldIdx)))
		}
		rows[i] = row
	}

	visible := selectVisibleColumns(cols, rows, terminalWidth())

	tw := tabwriter.NewWriter(w, 0, 0, columnSeparator, ' ', 0)
	defer tw.Flush()

	headers := make([]string, len(visible))
	for i, c := range visible {
		headers[i] = c.header
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))

	for _, row := range rows {
		out := make([]string, len(visible))
		for i, c := range visible {
			out[i] = row[c.colIdx]
		}
		fmt.Fprintln(tw, strings.Join(out, "\t"))
	}
	return nil
}

// column describes one rendered column derived from a struct field.
type column struct {
	header   string
	fieldIdx int
	colIdx   int // index into the full row before any hiding
	priority int
}

// tableColumns returns the columns derived from the `table` tag on each
// exported field. Tag form: "Header" or "Header,priority=N" (N=0-9).
// Fields without a tag, or with tag value "-", are excluded.
func tableColumns(t reflect.Type) []column {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	var cols []column
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("table")
		if tag == "" || tag == "-" {
			continue
		}
		header, prio := parseTableTag(tag)
		cols = append(cols, column{
			header:   header,
			fieldIdx: i,
			colIdx:   len(cols),
			priority: prio,
		})
	}
	return cols
}

// parseTableTag splits "Header,priority=N" into (Header, N). Missing or
// malformed priority defaults to defaultPriority. Out-of-range values are
// clamped to [0, 9].
func parseTableTag(tag string) (header string, priority int) {
	priority = defaultPriority
	parts := strings.Split(tag, ",")
	header = strings.TrimSpace(parts[0])
	for _, opt := range parts[1:] {
		opt = strings.TrimSpace(opt)
		const prefix = "priority="
		if !strings.HasPrefix(opt, prefix) {
			continue
		}
		n, err := strconv.Atoi(opt[len(prefix):])
		if err != nil {
			continue
		}
		if n < 0 {
			n = 0
		} else if n > 9 {
			n = 9
		}
		priority = n
	}
	return header, priority
}

// selectVisibleColumns drops the lowest-priority columns until the total
// content width fits within ttyWidth. If even the highest-priority subset
// overflows, returns all columns (caller's tabwriter handles overflow).
// Column order is preserved (only hides; never reorders).
func selectVisibleColumns(cols []column, rows [][]string, ttyWidth int) []column {
	if ttyWidth <= 0 {
		return cols
	}
	if fitsWidth(cols, rows, ttyWidth) {
		return cols
	}

	// Hide columns one at a time, lowest priority first; ties broken by
	// rightmost-first (later columns drop before earlier ones at same priority).
	visible := append([]column(nil), cols...)
	for len(visible) > 1 {
		dropAt := lowestPriorityIndex(visible)
		visible = append(visible[:dropAt], visible[dropAt+1:]...)
		if fitsWidth(visible, rows, ttyWidth) {
			return visible
		}
	}
	// Couldn't fit even with one column — fall back to original set.
	return cols
}

// lowestPriorityIndex returns the index of the lowest-priority column in
// visible. Ties broken by rightmost-first (higher index wins among ties).
func lowestPriorityIndex(visible []column) int {
	idx := 0
	for i := 1; i < len(visible); i++ {
		if visible[i].priority < visible[idx].priority {
			idx = i
			continue
		}
		if visible[i].priority == visible[idx].priority && i > idx {
			idx = i
		}
	}
	return idx
}

// fitsWidth reports whether the given columns fit within ttyWidth.
func fitsWidth(cols []column, rows [][]string, ttyWidth int) bool {
	return tableWidth(cols, rows) <= ttyWidth
}

// tableWidth is the sum of per-column max widths plus separator padding.
func tableWidth(cols []column, rows [][]string) int {
	if len(cols) == 0 {
		return 0
	}
	total := 0
	for i, c := range cols {
		w := len(c.header)
		for _, row := range rows {
			if cell := row[c.colIdx]; len(cell) > w {
				w = len(cell)
			}
		}
		total += w
		if i < len(cols)-1 {
			total += columnSeparator
		}
	}
	return total
}

// terminalWidth returns the current stdout terminal width, or fallbackWidth
// when stdout is not a TTY (pipes, redirects, tests).
func terminalWidth() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		return fallbackWidth
	}
	return w
}
