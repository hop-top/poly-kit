package output

import "github.com/charmbracelet/x/ansi"

// ellipsis marks a cell whose content was clipped to fit the width budget.
const ellipsis = "…"

// minCellWidth is the narrowest a column may be squeezed to before
// truncation stops reclaiming space from it. Below this a cell carries no
// information beyond the ellipsis itself.
const minCellWidth = 3

// displayWidth returns the rendered width of s in terminal cells, counting
// East Asian wide runes as two and ignoring any ANSI escape sequences
// introduced upstream by linkifyCell. Byte length and rune count both
// mismeasure these, which is why neither is used.
func displayWidth(s string) int { return ansi.StringWidth(s) }

// truncateCell clips s to at most width display cells, appending an ellipsis
// when it clips. It never splits a wide rune and never emits more than width
// cells; a string that already fits is returned unchanged.
func truncateCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, ellipsis)
}

// fitCellsToWidth clips the widest cells until the table fits within budget,
// returning a new row set and leaving the caller's slices untouched. Rows
// that already fit are returned as-is.
//
// Width is reclaimed from the widest columns first, so a single
// pathological cell is clipped back toward its neighbors rather than the
// neighbors being clipped or dropped on its behalf. Columns are never
// squeezed below their minimum width, so a budget smaller than the summed
// minimums leaves an overflow; selectVisibleColumns drops columns so that
// does not happen.
//
// Header text is honored as a floor per column (see columnMinWidths):
// clipping a body cell narrower than its own header buys nothing, since the
// header still sets the column's rendered width. Identifier columns are
// floored at their full width and so are never clipped.
func fitCellsToWidth(cols []column, rows [][]string, budget int) [][]string {
	if len(cols) == 0 || len(rows) == 0 || budget <= 0 {
		return rows
	}

	widths := columnWidths(cols, rows)
	total := 0
	for _, w := range widths {
		total += w
	}
	total += columnSeparator * (len(cols) - 1)
	if total <= budget {
		return rows
	}

	// Water-fill: find the largest cap L such that clipping every column to
	// max(floor, min(width, L)) fits the budget. The widest cells are clipped
	// first and ties clip together, so no column keeps slack while another
	// is squeezed. When even L=0 (every column at its floor) overflows, the
	// floors win and the caller has already dropped columns for that case.
	floors := columnMinWidths(cols, rows)
	fitted := func(limit int) ([]int, int) {
		out := make([]int, len(widths))
		sum := columnSeparator * (len(cols) - 1)
		for i, w := range widths {
			out[i] = max(floors[i], min(w, limit))
			sum += out[i]
		}
		return out, sum
	}
	limit := 0
	for _, w := range widths {
		limit = max(limit, w)
	}
	for limit > 0 {
		if _, sum := fitted(limit); sum <= budget {
			break
		}
		limit--
	}
	widths, _ = fitted(limit)

	out := make([][]string, len(rows))
	for i, row := range rows {
		clipped := make([]string, len(row))
		copy(clipped, row)
		for j, c := range cols {
			if cell := row[c.colIdx]; displayWidth(cell) > widths[j] {
				clipped[c.colIdx] = truncateCell(cell, widths[j])
			}
		}
		out[i] = clipped
	}
	return out
}

// columnMinWidths returns, parallel to cols, the narrowest each column can
// render: its header never shrinks, and body cells are not clipped below
// minCellWidth. An identifier column's minimum is its full width, so its
// cells are never clipped.
func columnMinWidths(cols []column, rows [][]string) []int {
	full := columnWidths(cols, rows)
	mins := make([]int, len(cols))
	for i, c := range cols {
		if c.ident {
			mins[i] = full[i]
			continue
		}
		mins[i] = max(minCellWidth, displayWidth(c.header))
	}
	return mins
}

// minContentWidth is the narrowest the columns can render given their
// minimums (see columnMinWidths), excluding separators and borders. mins is
// parallel to the full column set; columns are matched by colIdx.
func minContentWidth(cols []column, mins map[int]int) int {
	total := 0
	for _, c := range cols {
		total += mins[c.colIdx]
	}
	return total
}

// columnWidths returns the rendered width of each column: the widest of its
// header and any of its cells.
func columnWidths(cols []column, rows [][]string) []int {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = displayWidth(c.header)
		for _, row := range rows {
			if w := displayWidth(row[c.colIdx]); w > widths[i] {
				widths[i] = w
			}
		}
	}
	return widths
}
