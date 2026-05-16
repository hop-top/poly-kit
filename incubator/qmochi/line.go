package qmochi

import (
	"fmt"
	"strings"
)

// RenderLine renders a line chart for a single series.
func RenderLine(c Chart, ds Dataset, ly Layout) (string, error) {
	if len(ds.Series) != 1 {
		return "", fmt.Errorf("RenderLine supports exactly one series, got %d", len(ds.Series))
	}

	var b strings.Builder
	s := ds.Series[0]

	writeHeader(&b, c)

	domain, span := chartDomain(c, ds)

	plotHeight := ly.Plot.Height
	plotWidth := ly.Plot.Width

	grid := make([][]string, plotHeight)
	for i := range grid {
		grid[i] = make([]string, plotWidth)
		for j := range grid[i] {
			grid[i][j] = " "
		}
	}

	if len(ds.Labels) > 0 {
		colWidth := 1
		if len(ds.Labels) > 1 {
			colWidth = plotWidth / (len(ds.Labels) - 1)
		}

		lastRow := -1
		for i, p := range s.Points {
			val := p.Value
			valRow := int(float64(plotHeight-1) * (domain.Max - val) / span)
			cIdx := i * colWidth
			if cIdx >= plotWidth {
				cIdx = plotWidth - 1
			}

			// Plot point
			pointGlyph := "•"
			style := GetPointStyle(p, s, c.NoColor)
			grid[valRow][cIdx] = style.Render(pointGlyph)

			// Plot line from last point
			if lastRow != -1 {
				lastCIdx := (i - 1) * colWidth
				// horizontal line
				for x := lastCIdx + 1; x < cIdx; x++ {
					// simplified: just horizontal dash at the 'current' row for now
					grid[valRow][x] = "─"
				}
				// vertical connection if rows differ
				if valRow != lastRow {
					start := lastRow
					end := valRow
					if valRow < lastRow {
						start = valRow
						end = lastRow
					}
					for r := start + 1; r < end; r++ {
						grid[r][lastCIdx] = "│"
					}
				}
			}
			lastRow = valRow
		}
	}

	// Render grid
	for r := 0; r < plotHeight; r++ {
		if c.ShowYAxis {
			b.WriteString(strings.Repeat(" ", ly.Plot.X))
		}
		for cIdx := 0; cIdx < plotWidth; cIdx++ {
			b.WriteString(grid[r][cIdx])
		}
		b.WriteString("\n")
	}

	// 3. X-axis labels
	if c.ShowXAxis {
		b.WriteString(strings.Repeat(" ", ly.Plot.X))
		colWidth := 1
		if len(ds.Labels) > 1 {
			colWidth = plotWidth / (len(ds.Labels) - 1)
		}
		for i, label := range ds.Labels {
			// simplified: just place at the column start
			b.WriteString(label)
			if i < len(ds.Labels)-1 {
				gap := colWidth - len(label)
				if gap > 0 {
					b.WriteString(strings.Repeat(" ", gap))
				}
			}
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}
