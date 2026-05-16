package qmochi

import (
	"fmt"
	"strings"
)

// RenderLineBraille renders a high-resolution line chart using Braille characters.
func RenderLineBraille(c Chart, ds Dataset, ly Layout) (string, error) {
	if len(ds.Series) != 1 {
		return "", fmt.Errorf("RenderLineBraille supports exactly one series, got %d", len(ds.Series))
	}

	var b strings.Builder
	writeHeader(&b, c)

	domain, span := chartDomain(c, ds)

	plotHeight := ly.Plot.Height
	plotWidth := ly.Plot.Width

	canvas := NewBrailleCanvas(plotWidth, plotHeight)

	s := ds.Series[0]
	if len(s.Points) > 1 {
		for i := 0; i < len(s.Points)-1; i++ {
			p1 := s.Points[i]
			p2 := s.Points[i+1]

			x1 := float64(i) * float64(plotWidth*2-1) / float64(len(s.Points)-1)
			y1 := float64(plotHeight*4-1) * (domain.Max - p1.Value) / span

			x2 := float64(i+1) * float64(plotWidth*2-1) / float64(len(s.Points)-1)
			y2 := float64(plotHeight*4-1) * (domain.Max - p2.Value) / span

			// Simple line drawing between (x1, y1) and (x2, y2)
			steps := 10 // increase for smoother lines
			for t := 0; t <= steps; t++ {
				xt := x1 + (x2-x1)*float64(t)/float64(steps)
				yt := y1 + (y2-y1)*float64(t)/float64(steps)
				canvas.Set(int(xt), int(yt))
			}
		}
	}

	renderedPlot := canvas.Render()
	plotLines := strings.Split(strings.TrimSuffix(renderedPlot, "\n"), "\n")

	for _, line := range plotLines {
		if c.ShowYAxis {
			b.WriteString(strings.Repeat(" ", ly.Plot.X))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	// 3. X-axis labels
	if c.ShowXAxis && len(ds.Labels) > 0 {
		b.WriteString(strings.Repeat(" ", ly.Plot.X))
		// simplified placement
		for _, label := range ds.Labels {
			b.WriteString(label + " ")
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}
