package output

import (
	"encoding/json"
	"io"

	"gopkg.in/yaml.v3"
)

// Built-in formatters register against Default at package init.
//
// Each one preserves the exact behavior of the pre-Formatter code path so
// callers using output.Render(w, "json"|"yaml"|"table", v) see no change.
// Column projection (cols) and per-format options are honored where they
// apply; for json/yaml that means projecting struct values to maps keyed
// by `table:""` tag headers when cols is non-empty.
func init() {
	Default.Register(jsonFormatter{})
	Default.Register(yamlFormatter{})
	Default.Register(tableFormatter{})
}

type jsonFormatter struct{}

func (jsonFormatter) Key() string           { return JSON }
func (jsonFormatter) Extensions() []string  { return []string{".json"} }
func (jsonFormatter) Options() []OptionSpec { return nil }
func (jsonFormatter) Render(w io.Writer, data any, _ Options, cols []string) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if len(cols) == 0 {
		return enc.Encode(data)
	}
	return enc.Encode(projectToMaps(data, cols))
}

type yamlFormatter struct{}

func (yamlFormatter) Key() string           { return YAML }
func (yamlFormatter) Extensions() []string  { return []string{".yaml", ".yml"} }
func (yamlFormatter) Options() []OptionSpec { return nil }
func (yamlFormatter) Render(w io.Writer, data any, _ Options, cols []string) error {
	if len(cols) == 0 {
		return yaml.NewEncoder(w).Encode(data)
	}
	return yaml.NewEncoder(w).Encode(projectToMaps(data, cols))
}

type tableFormatter struct{}

func (tableFormatter) Key() string           { return Table }
func (tableFormatter) Extensions() []string  { return nil }
func (tableFormatter) Options() []OptionSpec { return nil }
func (tableFormatter) Render(w io.Writer, data any, _ Options, cols []string) error {
	return renderTable(w, data, cols)
}
