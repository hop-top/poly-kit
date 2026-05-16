package qmochi

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// RenderBar renders a horizontal bar chart.
func RenderBar(c Chart, ds Dataset, ly Layout) string {
	var b strings.Builder
	writeHeader(&b, c)

	domain, span := chartDomain(c, ds)

	// Compute label gutter from longest label.
	labelWidth := ly.Plot.X
	if labelWidth == 0 {
		for _, l := range ds.Labels {
			if len(l) > labelWidth {
				labelWidth = len(l)
			}
		}
		if labelWidth > 0 {
			labelWidth++ // space after label
		}
	}

	for i, label := range ds.Labels {
		labelStr := lipgloss.NewStyle().Width(labelWidth).Render(label)
		b.WriteString(labelStr)

		// Plot area
		plotWidth := ly.Plot.Width
		for _, s := range ds.Series {
			p := s.Points[i]

			val := p.Value
			valPos := float64(plotWidth) * (val - domain.Min) / span
			zeroPos := float64(0)
			if domain.Min < 0 && domain.Max > 0 {
				zeroPos = float64(plotWidth) * (-domain.Min / span)
			} else if domain.Min < 0 {
				zeroPos = float64(plotWidth)
			}

			// Render bar with fractional blocks
			horizontalBlocks := GetHorizontalPalette(ResolveStyle(c.Style, s.Style))

			var bar string
			start := math.Min(zeroPos, valPos)
			end := math.Max(zeroPos, valPos)

			// Fill spaces before bar
			b.WriteString(strings.Repeat(" ", int(start)))

			// Iterate through cells
			for x := int(start); x < plotWidth; x++ {
				cellStart := float64(x)
				cellEnd := float64(x + 1)

				if cellEnd <= start || cellStart >= end {
					if x >= int(end) {
						b.WriteString(" ")
					}
					continue
				}

				// Calculate coverage of this cell [cellStart, cellEnd] by [start, end]
				covStart := math.Max(cellStart, start)
				covEnd := math.Min(cellEnd, end)
				coverage := covEnd - covStart

				if coverage >= 1.0 {
					bar = horizontalBlocks[8] // full block from palette
				} else {
					idx := int(coverage * 8)
					bar = horizontalBlocks[idx]
				}

				style := GetPointStyle(p, s, c.NoColor)
				b.WriteString(style.Render(bar))
			}

			if c.ShowValues {
				fmt.Fprintf(&b, " %.2f", val)
			}
		}
		b.WriteString("\n")
	}

	// 3. Render Legend
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
