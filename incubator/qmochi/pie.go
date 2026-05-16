package qmochi

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// RenderPie renders a circular pie chart.
// It uses a single series where each point is a slice.
func RenderPie(c Chart, ds Dataset, ly Layout) (string, error) {
	if len(ds.Series) != 1 {
		// If multiple series, we could technically render multiple pies or a nested pie,
		// but for v1 we'll stick to one.
		return "", nil
	}

	var b strings.Builder
	s := ds.Series[0]

	writeHeader(&b, c)

	// 2. Prepare Data
	total := 0.0
	for _, p := range s.Points {
		total += p.Value
	}

	if total <= 0 {
		return "No data to display", nil
	}

	// Calculate slice angles
	type slice struct {
		label  string
		color  string
		effect Effect
		start  float64
		end    float64
	}
	slices := make([]slice, len(s.Points))
	currentAngle := 0.0
	for i, p := range s.Points {
		angle := (p.Value / total) * 2 * math.Pi
		color := p.Color
		if color == "" {
			color = s.Color
		}
		effect := p.Effect
		if effect == NoEffect {
			effect = s.Effect
		}
		slices[i] = slice{
			label:  p.Label,
			color:  color,
			effect: effect,
			start:  currentAngle,
			end:    currentAngle + angle,
		}
		currentAngle += angle
	}

	// 3. Render Grid
	plotHeight := ly.Plot.Height
	plotWidth := ly.Plot.Width

	// Terminal cells are ~2:1 aspect ratio (taller than wide).
	aspectRatio := 2.0

	centerX := float64(plotWidth) / 2.0
	centerY := float64(plotHeight) / 2.0
	radius := math.Min(centerX, centerY*aspectRatio) - 1.0

	for y := 0; y < plotHeight; y++ {
		if c.ShowYAxis {
			b.WriteString(strings.Repeat(" ", ly.Plot.X))
		}
		for x := 0; x < plotWidth; x++ {
			dx := (float64(x) - centerX)
			dy := (float64(y) - centerY) * aspectRatio
			dist := math.Sqrt(dx*dx + dy*dy)

			if dist <= radius {
				angle := math.Atan2(dx, -dy)
				if angle < 0 {
					angle += 2 * math.Pi
				}

				found := false
				for si, sl := range slices {
					if angle >= sl.start && angle < sl.end {
						if c.NoColor {
							b.WriteString(CategoryGlyphs[si%len(CategoryGlyphs)])
						} else {
							style := lipgloss.NewStyle().Foreground(lipgloss.Color(sl.color))
							style = ApplyEffects(style, sl.effect)
							b.WriteString(style.Render("█"))
						}
						found = true
						break
					}
				}
				if !found {
					b.WriteString(" ")
				}
			} else {
				b.WriteString(" ")
			}
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}
