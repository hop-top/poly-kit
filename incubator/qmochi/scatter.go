package qmochi

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// RenderScatter renders an X/Y scatter plot.
// Each series uses a distinct marker glyph from MarkerGlyphs.
func RenderScatter(c Chart, ds Dataset, ly Layout) string {
	var b strings.Builder
	writeHeader(&b, c)

	// Legend (top, like the reference image)
	if c.ShowLegend && len(ds.Series) > 0 {
		var parts []string
		for i, s := range ds.Series {
			marker := MarkerGlyphs[i%len(MarkerGlyphs)]
			if !c.NoColor && s.Color != "" {
				marker = lipgloss.NewStyle().
					Foreground(lipgloss.Color(s.Color)).Render(marker)
			}
			parts = append(parts, fmt.Sprintf("%s=%s", s.Name, marker))
		}
		b.WriteString(strings.Join(parts, "  "))
		b.WriteString("\n")
	}

	plotHeight := ly.Plot.Height
	plotWidth := ly.Plot.Width
	if plotHeight <= 0 || plotWidth <= 0 {
		return b.String()
	}

	// Compute X/Y domains from all points.
	xMin, xMax := math.MaxFloat64, -math.MaxFloat64
	yMin, yMax := math.MaxFloat64, -math.MaxFloat64
	hasData := false
	for _, s := range ds.Series {
		for _, p := range s.Points {
			if p.X < xMin {
				xMin = p.X
			}
			if p.X > xMax {
				xMax = p.X
			}
			if p.Value < yMin {
				yMin = p.Value
			}
			if p.Value > yMax {
				yMax = p.Value
			}
			hasData = true
		}
	}
	if !hasData {
		// Empty grid
		for y := 0; y < plotHeight; y++ {
			b.WriteString(strings.Repeat(" ", plotWidth))
			b.WriteString("\n")
		}
		return b.String()
	}

	xSpan := xMax - xMin
	if xSpan == 0 {
		xSpan = 1
	}
	ySpan := yMax - yMin
	if ySpan == 0 {
		ySpan = 1
	}

	// Build grid
	grid := make([][]string, plotHeight)
	for y := range grid {
		grid[y] = make([]string, plotWidth)
		for x := range grid[y] {
			grid[y][x] = " "
		}
	}

	// Plot points (later series overwrite earlier on collision)
	for si, s := range ds.Series {
		marker := MarkerGlyphs[si%len(MarkerGlyphs)]
		if !c.NoColor && s.Color != "" {
			marker = lipgloss.NewStyle().
				Foreground(lipgloss.Color(s.Color)).Render(marker)
		}
		for _, p := range s.Points {
			col := int(math.Round((p.X - xMin) / xSpan * float64(plotWidth-1)))
			row := plotHeight - 1 - int(math.Round((p.Value-yMin)/ySpan*float64(plotHeight-1)))
			if col < 0 {
				col = 0
			}
			if col >= plotWidth {
				col = plotWidth - 1
			}
			if row < 0 {
				row = 0
			}
			if row >= plotHeight {
				row = plotHeight - 1
			}
			grid[row][col] = marker
		}
	}

	// Y-axis gutter
	gutter := 0
	if c.ShowYAxis {
		ticks := NiceTicks(Domain{Min: yMin, Max: yMax}, 5)
		for _, t := range ticks {
			if len(t.Label) > gutter {
				gutter = len(t.Label)
			}
		}
		gutter++ // space after label
	}

	// Render grid rows
	for y := 0; y < plotHeight; y++ {
		if c.ShowYAxis {
			// Map row to value for tick label
			val := yMax - (float64(y)/float64(plotHeight-1))*ySpan
			label := ""
			ticks := NiceTicks(Domain{Min: yMin, Max: yMax}, 5)
			for _, t := range ticks {
				if math.Abs(t.Value-val) < ySpan/float64(plotHeight)/2 {
					label = t.Label
					break
				}
			}
			b.WriteString(lipgloss.NewStyle().Width(gutter).Render(label))
		}

		// Y-axis line
		if c.ShowYAxis {
			b.WriteString("│")
		}

		for x := 0; x < plotWidth; x++ {
			b.WriteString(grid[y][x])
		}
		b.WriteString("\n")
	}

	// X-axis
	if c.ShowXAxis {
		if gutter > 0 {
			b.WriteString(strings.Repeat(" ", gutter))
		}
		b.WriteString("└")
		b.WriteString(strings.Repeat("─", plotWidth))
		b.WriteString("\n")
	}

	return b.String()
}
