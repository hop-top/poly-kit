package output

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// FormatSummary is the per-format row rendered by ListFormats. It
// intentionally has `table:""` tags so we can dogfood the table
// formatter for --format-help output.
type FormatSummary struct {
	Key        string `json:"key"        yaml:"key"        table:"FORMAT,priority=9"`
	Extensions string `json:"extensions" yaml:"extensions" table:"EXTENSIONS,priority=7"`
	Options    string `json:"options"    yaml:"options"    table:"OPTIONS,priority=5"`
}

// OptionRow describes a single OptionSpec, used by FormatOptions to render
// per-format help. Same dogfooding rationale as FormatSummary.
type OptionRow struct {
	Name    string `json:"name"    yaml:"name"    table:"NAME,priority=9"`
	Type    string `json:"type"    yaml:"type"    table:"TYPE,priority=7"`
	Default string `json:"default" yaml:"default" table:"DEFAULT,priority=6"`
	Enum    string `json:"enum"    yaml:"enum"    table:"ENUM,priority=4"`
	Usage   string `json:"usage"   yaml:"usage"    table:"USAGE,priority=3"`
}

// ListFormats returns one FormatSummary per registered formatter, sorted
// by key. Used by --format-help with no argument.
func ListFormats(r *Registry) []FormatSummary {
	if r == nil {
		r = Default
	}
	formatters := r.Formatters()
	out := make([]FormatSummary, 0, len(formatters))
	for _, f := range formatters {
		opts := make([]string, 0, len(f.Options()))
		for _, s := range f.Options() {
			opts = append(opts, s.Name)
		}
		sort.Strings(opts)
		out = append(out, FormatSummary{
			Key:        f.Key(),
			Extensions: strings.Join(f.Extensions(), ", "),
			Options:    strings.Join(opts, ", "),
		})
	}
	return out
}

// FormatOptions returns one OptionRow per OptionSpec on the formatter
// registered under key. Returns an error when key is unknown.
func FormatOptions(r *Registry, key string) ([]OptionRow, error) {
	if r == nil {
		r = Default
	}
	f, ok := r.Lookup(key)
	if !ok {
		return nil, fmt.Errorf("unknown format %q (valid: %s)",
			key, strings.Join(r.Keys(), ", "))
	}
	specs := f.Options()
	out := make([]OptionRow, 0, len(specs))
	for _, s := range specs {
		def := ""
		if s.Default != nil {
			def = fmt.Sprintf("%v", s.Default)
		}
		out = append(out, OptionRow{
			Name:    s.Name,
			Type:    optionTypeName(s.Type),
			Default: def,
			Enum:    strings.Join(s.Enum, ", "),
			Usage:   s.Usage,
		})
	}
	return out, nil
}

// RenderFormatHelp writes --format-help output to w. When format is
// empty, it lists all formatters; otherwise it lists that formatter's
// options. Reuses output.Render(table) so the help itself respects the
// output package's rendering pipeline.
func RenderFormatHelp(w io.Writer, r *Registry, format string) error {
	if r == nil {
		r = Default
	}
	if format == "" {
		return Render(w, Table, ListFormats(r))
	}
	rows, err := FormatOptions(r, format)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintf(w, "format %q has no options\n", format)
		return nil
	}
	return Render(w, Table, rows)
}

func optionTypeName(t OptionType) string {
	switch t {
	case OptString:
		return "string"
	case OptInt:
		return "int"
	case OptBool:
		return "bool"
	case OptEnum:
		return "enum"
	default:
		return "unknown"
	}
}
