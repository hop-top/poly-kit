package qmochi

import (
	"strings"

	"charm.land/lipgloss/v2"
)

var heatmapPalette = []string{
	"#161b22", // level 0 (dark bg)
	"#0e4429", // level 1
	"#006d32", // level 2
	"#26a641", // level 3
	"#39d353", // level 4
}

// RenderHeatmap renders a 2D heatmap.
// Each Series in ds represents a row; ds.Labels are column headers.
//
// When Compact is true, two data rows are packed into one terminal
// line using half-block characters (▄), with foreground = bottom
// row and background = top row. This eliminates vertical gaps.
//
// Row labels render only when the series Name is non-empty.
// When ShowXAxis is true, column labels render below the grid;
// empty labels are skipped for sparse month markers.
func RenderHeatmap(c Chart, ds Dataset, ly Layout) string {
	var b strings.Builder
	writeHeader(&b, c)

	domain, span := chartDomain(c, ds)

	gutter := computeGutter(c, ds)

	if c.Compact {
		renderCompact(&b, c, ds, domain, span, gutter)
	} else {
		renderStandard(&b, c, ds, domain, span, gutter)
	}

	renderXAxis(&b, c, ds, gutter)

	return b.String()
}

func paletteIndex(val, min, span float64) int {
	if val <= 0 {
		return 0
	}
	idx := int(((val - min) / span) * float64(len(heatmapPalette)-1))
	if idx < 1 {
		idx = 1
	}
	if idx >= len(heatmapPalette) {
		idx = len(heatmapPalette) - 1
	}
	return idx
}

func computeGutter(c Chart, ds Dataset) int {
	if !c.ShowYAxis {
		return 0
	}
	gutter := 0
	for _, s := range ds.Series {
		if len(s.Name) > gutter {
			gutter = len(s.Name)
		}
	}
	return gutter + 1
}

// renderStandard renders one terminal line per data row.
func renderStandard(b *strings.Builder, c Chart, ds Dataset, domain Domain, span float64, gutter int) {
	for _, s := range ds.Series {
		if c.ShowYAxis {
			b.WriteString(lipgloss.NewStyle().Width(gutter).Render(s.Name))
		}
		for _, p := range s.Points {
			idx := paletteIndex(p.Value, domain.Min, span)
			if c.NoColor {
				b.WriteString(IntensityGlyphs[idx])
			} else {
				cell := c.CellGlyph
				if cell == "" {
					cell = "█"
				}
				style := lipgloss.NewStyle().Foreground(lipgloss.Color(heatmapPalette[idx]))
				effect := p.Effect
				if effect == NoEffect {
					effect = s.Effect
				}
				style = ApplyEffects(style, effect)
				b.WriteString(style.Render(cell))
			}
		}
		b.WriteString("\n")
	}
}

// renderCompact packs two data rows per terminal line using ▄.
// Foreground = bottom row color, background = top row color.
// Odd row count: last row pairs with an implicit empty row
// (all palette[0]) so every row renders at the same height.
func renderCompact(b *strings.Builder, c Chart, ds Dataset, domain Domain, span float64, gutter int) {
	rows := ds.Series
	pairEnd := len(rows)
	if pairEnd%2 != 0 {
		pairEnd--
	}

	// Paired rows: ▄ with bg=top, fg=bottom
	for i := 0; i < pairEnd; i += 2 {
		top := rows[i]
		bot := rows[i+1]

		if c.ShowYAxis {
			label := top.Name
			if label == "" {
				label = bot.Name
			}
			b.WriteString(lipgloss.NewStyle().Width(gutter).Render(label))
		}

		cols := len(top.Points)
		if len(bot.Points) > cols {
			cols = len(bot.Points)
		}

		for j := 0; j < cols; j++ {
			topIdx := 0
			if j < len(top.Points) {
				topIdx = paletteIndex(top.Points[j].Value, domain.Min, span)
			}
			botIdx := 0
			if j < len(bot.Points) {
				botIdx = paletteIndex(bot.Points[j].Value, domain.Min, span)
			}
			if c.NoColor {
				// Use top row shade (compact NoColor loses bottom row)
				b.WriteString(IntensityGlyphs[topIdx])
			} else {
				style := lipgloss.NewStyle().
					Foreground(lipgloss.Color(heatmapPalette[botIdx])).
					Background(lipgloss.Color(heatmapPalette[topIdx]))
				b.WriteString(style.Render("▄"))
			}
		}
		b.WriteString("\n")
	}

	// Odd last row: ▀ with fg=data (top half), no bg (fades to terminal)
	if len(rows)%2 != 0 {
		last := rows[len(rows)-1]
		if c.ShowYAxis {
			b.WriteString(lipgloss.NewStyle().Width(gutter).Render(last.Name))
		}
		for _, p := range last.Points {
			idx := paletteIndex(p.Value, domain.Min, span)
			if c.NoColor {
				b.WriteString(IntensityGlyphs[idx])
			} else {
				style := lipgloss.NewStyle().
					Foreground(lipgloss.Color(heatmapPalette[idx]))
				effect := p.Effect
				if effect == NoEffect {
					effect = last.Effect
				}
				style = ApplyEffects(style, effect)
				b.WriteString(style.Render("▀"))
			}
		}
		b.WriteString("\n")
	}
}

func renderXAxis(b *strings.Builder, c Chart, ds Dataset, gutter int) {
	if !c.ShowXAxis {
		return
	}
	xlabels := c.XLabels
	if len(xlabels) == 0 {
		xlabels = ds.Labels
	}
	if len(xlabels) == 0 {
		return
	}
	if gutter > 0 {
		b.WriteString(strings.Repeat(" ", gutter))
	}
	col := 0
	for col < len(xlabels) {
		label := xlabels[col]
		if label == "" {
			b.WriteString(" ")
			col++
		} else {
			b.WriteString(label)
			col += len(label)
		}
	}
	b.WriteString("\n")
}
