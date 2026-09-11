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
// Width is reclaimed from the widest column first, repeatedly, so a single
// pathological cell is clipped back toward its neighbors rather than the
// neighbors being clipped or dropped on its behalf. Columns are never
// squeezed below minCellWidth, so a budget too small for the whole set
// leaves a residual overflow for the caller's renderer to wrap — that is
// strictly better than silently deleting data.
//
// Header text is honored as a floor per column: clipping a body cell
// narrower than its own header buys nothing, since the header still sets
// the column's rendered width.
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

	// Reclaim from the widest column each pass. Shrinking the widest by one
	// cell at a time would be O(overflow); instead drop it to the width of
	// the runner-up, which converges in at most len(cols) passes.
	for total > budget {
		widest, second := 0, 0
		for i, w := range widths {
			if w > widths[widest] {
				widest, second = i, widest
			} else if i != widest && w > widths[second] {
				second = i
			}
		}
		floor := max(minCellWidth, displayWidth(cols[widest].header))
		target := max(floor, widths[second])
		if target >= widths[widest] {
			// Every column is already at its floor; the remainder cannot
			// be reclaimed without destroying information.
			break
		}
		if reclaimed := widths[widest] - target; reclaimed > total-budget {
			target = widths[widest] - (total - budget)
		}
		total -= widths[widest] - target
		widths[widest] = target
	}

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
