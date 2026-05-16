package qmochi

// PlotRect represents the area where data is plotted.
type PlotRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Layout contains all computed layout information for a chart.
type Layout struct {
	Plot    PlotRect
	XTicks  []Tick
	YTicks  []Tick
	ZeroRow int // Y-coordinate of the zero line relative to plot top
}

// LayoutFor computes the layout for a given chart and dataset.
func LayoutFor(c Chart, ds Dataset) Layout {
	width := c.Size.Width
	height := c.Size.Height

	if width <= 0 || height <= 0 {
		return Layout{}
	}

	domain := DomainFor(ds)

	// Default plot area
	plot := PlotRect{
		X:      0,
		Y:      0,
		Width:  width,
		Height: height,
	}

	// 1. Reserve space for Title and Subtitle
	if c.Title != "" {
		plot.Y++
		plot.Height--
	}
	if c.Subtitle != "" {
		plot.Y++
		plot.Height--
	}

	// 2. Reserve bottom gutter for X labels
	var xTicks []Tick
	if c.ShowXAxis && plot.Height > 0 {
		plot.Height-- // Space for X labels

		// Map dataset labels to ticks
		if len(ds.Labels) > 0 {
			// This is a simplification; actual placement depends on chart type
			// (e.g., center of bar vs point on line).
			// For now, we just provide the labels.
			for i, label := range ds.Labels {
				xTicks = append(xTicks, Tick{
					Value: float64(i),
					Label: label,
				})
			}
		}
	}

	// 3. Reserve left gutter for Y labels
	var yTicks []Tick
	if c.ShowYAxis && plot.Width > 0 {
		gutter := 0
		if c.Type == HeatmapChart {
			// For heatmap, Y labels are series names
			maxNameLen := 0
			for _, s := range ds.Series {
				if len(s.Name) > maxNameLen {
					maxNameLen = len(s.Name)
				}
			}
			gutter = maxNameLen + 1
		} else {
			// Generate nice ticks for Y axis
			yTicks = NiceTicks(domain, 5) // Default to max 5 ticks

			maxLabelLen := 0
			for _, t := range yTicks {
				if len(t.Label) > maxLabelLen {
					maxLabelLen = len(t.Label)
				}
			}
			gutter = maxLabelLen + 1 // +1 for spacing
		}

		if gutter > plot.Width {
			gutter = plot.Width
		}

		plot.X += gutter
		plot.Width -= gutter
	}

	// Ensure non-negative dimensions
	if plot.Width < 0 {
		plot.Width = 0
	}
	if plot.Height < 0 {
		plot.Height = 0
	}

	// 4. Calculate ZeroRow
	zeroRow := plot.Height // Default to bottom (positive-only)
	if domain.Max <= 0 {
		zeroRow = 0 // Top (negative-only)
	} else if domain.Min < 0 && domain.Max > 0 {
		// Mixed sign: map 0 to row
		// formula: row = height * (max - 0) / (max - min)
		span := domain.Max - domain.Min
		if span > 0 {
			zeroRow = int(float64(plot.Height) * (domain.Max / span))
		}
	}

	return Layout{
		Plot:    plot,
		XTicks:  xTicks,
		YTicks:  yTicks,
		ZeroRow: zeroRow,
	}
}
