package output

import (
	"fmt"
	"io"
)

// HumanRenderer is implemented by data types that ship a bespoke
// human-readable rendering. The human formatter dispatches to
// RenderHuman when data satisfies this interface; otherwise it falls
// back to the table formatter so adopters who pass --format=human on
// types without a custom renderer still get a usable view.
//
// Adopters reach for HumanRenderer when their output is finding-stream
// shaped (per-record annotated lines, suggestions, footers) and does
// not fit the `table:""` row model. Examples in tree:
// verify-no-leak, verify-stories, grade.
type HumanRenderer interface {
	RenderHuman(w io.Writer) error
}

// humanFormatter is registered as the "human" entry in the Default
// registry. Render delegates to data.RenderHuman when available; falls
// back to table rendering otherwise so the format key always produces
// some output rather than erroring.
type humanFormatter struct{}

func init() {
	Default.Register(humanFormatter{})
}

func (humanFormatter) Key() string          { return Human }
func (humanFormatter) Extensions() []string { return nil }
func (humanFormatter) Options() []OptionSpec {
	return nil
}

func (humanFormatter) Render(w io.Writer, data any, _ Options, cols []string) error {
	if hr, ok := data.(HumanRenderer); ok {
		return hr.RenderHuman(w)
	}
	// Fallback: try table rendering. Types with `table:""` tags get a
	// reasonable view; types without tags emit nothing (matches table
	// formatter behavior).
	if err := renderTable(w, data, cols); err != nil {
		return fmt.Errorf("human formatter: no RenderHuman for %T and table fallback failed: %w", data, err)
	}
	return nil
}
