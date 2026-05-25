package output

import (
	"fmt"
	"io"
	"os"
	"reflect"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/mattn/go-isatty"
)

// renderStyledTable writes v as a lipgloss-backed table using style. It is
// the styled counterpart to renderTable; column extraction and projection
// follow the same `table:""` tag rules so callers see no schema difference
// between modes.
//
// emphasis is a row-index → EmphasisKind map populated by RowEmphasis
// options. Missing entries render with default (uncolored) text.
func renderStyledTable(w io.Writer, v any, selected []string, style TableStyle, emphasis map[int]EmphasisKind) error {
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
			if e.Kind() == reflect.Pointer {
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
	headers := make([]string, len(visible))
	projected := make([][]string, len(rows))
	for i, c := range visible {
		headers[i] = c.header
	}
	for i, row := range rows {
		out := make([]string, len(visible))
		for j, c := range visible {
			out[j] = row[c.colIdx]
		}
		projected[i] = out
	}

	border := style.Border
	if isZeroBorder(border) {
		border = lipgloss.NormalBorder()
	}

	t := table.New().
		Headers(headers...).
		Rows(projected...).
		Width(terminalWidth()).
		Border(border).
		BorderRow(false).
		BorderColumn(true).
		BorderHeader(true).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				s := lipgloss.NewStyle()
				if style.Header != nil {
					s = s.Bold(true).Foreground(style.Header)
				}
				return s
			}
			kind := EmphasisNone
			if emphasis != nil {
				kind = emphasis[row]
			}
			switch kind {
			case EmphasisPrimary:
				if style.Primary != nil {
					return lipgloss.NewStyle().Foreground(style.Primary)
				}
			case EmphasisSecondary:
				if style.Secondary != nil {
					return lipgloss.NewStyle().Foreground(style.Secondary)
				}
			case EmphasisMuted:
				if style.Muted != nil {
					return lipgloss.NewStyle().Foreground(style.Muted)
				}
			}
			return lipgloss.NewStyle()
		})

	if style.BorderForeground != nil {
		t = t.BorderStyle(lipgloss.NewStyle().Foreground(style.BorderForeground))
	}

	_, err := fmt.Fprintln(w, t.Render())
	return err
}

// writerIsTTY reports whether w is an *os.File attached to a terminal.
// Buffers, pipes, and non-file writers return false. Mirrors the check
// used in hint.RenderHints so styled output behaves consistently across
// the package.
func writerIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd())
}
