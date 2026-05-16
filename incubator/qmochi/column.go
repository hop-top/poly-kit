package qmochi

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// RenderColumn renders a vertical column chart.
func RenderColumn(c Chart, ds Dataset, ly Layout) string {
	var b strings.Builder
	writeHeader(&b, c)

	domain, span := chartDomain(c, ds)

	plotHeight := ly.Plot.Height
	plotWidth := ly.Plot.Width

	// Grid for the plot area
	grid := make([][]string, plotHeight)
	for i := range grid {
		grid[i] = make([]string, plotWidth)
		for j := range grid[i] {
			grid[i][j] = " "
		}
	}

	// Calculate column width (simple: equal width for each label)
	if len(ds.Labels) > 0 {
		colWidth := plotWidth / len(ds.Labels)

		for i := range ds.Labels {
			for _, s := range ds.Series {
				p := s.Points[i]
				val := p.Value

				// Map value to row
				valRowFloat := float64(plotHeight-1) * (domain.Max - val) / span
				zeroRowFloat := float64(ly.ZeroRow)

				// Fill column with fractional blocks
				verticalBlocks := GetVerticalPalette(ResolveStyle(c.Style, s.Style))

				start := math.Min(zeroRowFloat, valRowFloat)
				end := math.Max(zeroRowFloat, valRowFloat)

				for r := 0; r < plotHeight; r++ {
					cellStart := float64(r)
					cellEnd := float64(r + 1)

					if cellEnd <= start || cellStart >= end {
						continue
					}

					covStart := math.Max(cellStart, start)
					covEnd := math.Min(cellEnd, end)
					coverage := covEnd - covStart

					var glyph string
					if coverage >= 1.0 {
						glyph = verticalBlocks[8]
					} else {
						idx := int(coverage * 8)
						glyph = verticalBlocks[idx]
					}

					style := GetPointStyle(p, s, c.NoColor)

					cIdx := i*colWidth + colWidth/2
					if cIdx < plotWidth {
						grid[r][cIdx] = style.Render(glyph)
					}
				}
			}
		}
	}

	// Render grid to string builder
	for r := 0; r < plotHeight; r++ {
		// Y-axis labels if needed
		if c.ShowYAxis {
			// Find tick for this row
			// simplified: just space for now
			b.WriteString(strings.Repeat(" ", ly.Plot.X))
		}

		for cIdx := 0; cIdx < plotWidth; cIdx++ {
			b.WriteString(grid[r][cIdx])
		}
		b.WriteString("\n")
	}

	// 3. Render X-axis labels
	if c.ShowXAxis {
		b.WriteString(strings.Repeat(" ", ly.Plot.X))
		colWidth := 1
		if len(ds.Labels) > 0 {
			colWidth = plotWidth / len(ds.Labels)
		}

		for _, label := range ds.Labels {
			labelStyle := lipgloss.NewStyle().Width(colWidth).Align(lipgloss.Center)
			b.WriteString(labelStyle.Render(label))
		}
		b.WriteString("\n")
	}

	// 4. Render Legend
	if c.ShowLegend && len(ds.Series) > 0 {
		b.WriteString("\n")
		for _, s := range ds.Series {
			swatch := "█"
			if !c.NoColor && s.Color != "" {
				swatch = lipgloss.NewStyle().Foreground(lipgloss.Color(s.Color)).Render(swatch)
			}
			fmt.Fprintf(&b, "%s %s  ", swatch, s.Name)
		}
		b.WriteString("\n")
	}

	return b.String()
}
