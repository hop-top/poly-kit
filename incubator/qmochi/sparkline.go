package qmochi

import (
	"strings"
)

// RenderSparkline renders a compact line chart for a single series.
// Palette: ▁▂▃▄▅▆▇█ (Note: task description used ▁ as first, maybe 0?)
func RenderSparkline(series Series, width int) string {
	if width <= 0 || len(series.Points) == 0 {
		return strings.Repeat(" ", width)
	}

	palette := GetVerticalPalette(SolidBlock)

	// Normalize points to width
	points := series.Points
	if len(points) > width {
		// Downsample if too many points
		// Simplified: just take first width points
		points = points[:width]
	}

	min := points[0].Value
	max := points[0].Value
	for _, p := range points {
		if p.Value < min {
			min = p.Value
		}
		if p.Value > max {
			max = p.Value
		}
	}

	span := max - min
	if span == 0 {
		span = 1
	}

	var b strings.Builder
	for i := 0; i < width; i++ {
		if i < len(points) {
			val := points[i].Value
			idx := int(float64(len(palette)-1) * (val - min) / span)
			if idx < 0 {
				idx = 0
			}
			if idx >= len(palette) {
				idx = len(palette) - 1
			}
			b.WriteString(palette[idx])
		} else {
			b.WriteString(" ")
		}
	}

	return b.String()
}
