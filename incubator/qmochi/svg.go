package qmochi

import (
	"fmt"
	"math"
	"strings"
)

// Default SVG color palette when series have no color set.
var svgPalette = []string{
	"#4285F4", "#EA4335", "#FBBC05", "#34A853",
	"#FF6D01", "#46BDC6", "#7BAAF7", "#F07B72",
}

// RenderSVG renders a chart as an SVG string. The chart type
// determines the visual representation:
//   - BarChart: horizontal <rect> bars
//   - LineChart: <polyline> paths
//   - ScatterChart: <circle> points
//   - ColumnChart: vertical <rect> bars
//
// Size.Width and Size.Height control the SVG viewBox in pixels.
func RenderSVG(c Chart, ds Dataset) string {
	w := c.Size.Width
	h := c.Size.Height
	if w <= 0 {
		w = 400
	}
	if h <= 0 {
		h = 200
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version='1.0'?>
<svg xmlns='http://www.w3.org/2000/svg' width='%d' height='%d' version='1.1'>
`, w, h)

	// Margins for axes
	left, top, right, bottom := 0, 0, 0, 0
	if c.ShowYAxis {
		left = 50
	}
	if c.ShowXAxis {
		bottom = 25
	}
	if c.ShowLegend {
		right = 120
	}
	if c.Title != "" {
		top = 20
	}

	plotW := w - left - right
	plotH := h - top - bottom

	// Title
	if c.Title != "" {
		fmt.Fprintf(&b, "  <text x='%d' y='14' font-size='12' font-weight='bold'>%s</text>\n",
			left, c.Title)
	}

	// Axes
	if c.ShowYAxis || c.ShowXAxis {
		renderSVGAxes(&b, c, ds, left, top, plotW, plotH)
	}

	switch c.Type {
	case BarChart:
		renderSVGBars(&b, c, ds, left, top, plotW, plotH)
	case ColumnChart:
		renderSVGColumns(&b, c, ds, left, top, plotW, plotH)
	case LineChart:
		renderSVGLines(&b, c, ds, left, top, plotW, plotH)
	case ScatterChart:
		renderSVGScatter(&b, c, ds, left, top, plotW, plotH)
	case PieChart:
		renderSVGPie(&b, c, ds, left, top, plotW, plotH)
	case HeatmapChart:
		renderSVGHeatmap(&b, c, ds, left, top, plotW, plotH)
	case SparklineChart:
		renderSVGSparkline(&b, c, ds, left, top, plotW, plotH)
	}

	// Legend
	if c.ShowLegend {
		renderSVGLegend(&b, c, ds, left+plotW+10, top)
	}

	b.WriteString("</svg>\n")
	return b.String()
}

func svgColor(s Series, idx int) string {
	if s.Color != "" {
		return s.Color
	}
	return svgPalette[idx%len(svgPalette)]
}

func renderSVGAxes(b *strings.Builder, c Chart, ds Dataset, left, top, plotW, plotH int) {
	// Y-axis line
	if c.ShowYAxis {
		fmt.Fprintf(b, "  <line x1='%d' y1='%d' x2='%d' y2='%d' stroke='#888' />\n",
			left, top, left, top+plotH)

		domain, _ := chartDomain(c, ds)
		ticks := NiceTicks(domain, 5)
		for _, t := range ticks {
			span := domain.Max - domain.Min
			if span == 0 {
				span = 1
			}
			y := top + plotH - int(float64(plotH)*((t.Value-domain.Min)/span))
			fmt.Fprintf(b, "  <text x='%d' y='%d' font-size='10' text-anchor='end'>%s</text>\n",
				left-5, y+4, t.Label)
		}
	}

	// X-axis line
	if c.ShowXAxis {
		fmt.Fprintf(b, "  <line x1='%d' y1='%d' x2='%d' y2='%d' stroke='#888' />\n",
			left, top+plotH, left+plotW, top+plotH)
	}
}

func renderSVGBars(b *strings.Builder, c Chart, ds Dataset, left, top, plotW, plotH int) {
	domain, span := chartDomain(c, ds)
	nLabels := len(ds.Labels)
	if nLabels == 0 {
		return
	}
	barH := plotH / nLabels

	for li, label := range ds.Labels {
		y := top + li*barH
		seriesH := barH / len(ds.Series)
		for si, s := range ds.Series {
			val := s.Points[li].Value
			barW := int(float64(plotW) * (val - domain.Min) / span)
			color := svgColor(s, si)
			fmt.Fprintf(b, "  <rect x='%d' y='%d' width='%d' height='%d' fill='%s' />\n",
				left, y+si*seriesH, barW, seriesH-1, color)
		}
		if c.ShowXAxis {
			fmt.Fprintf(b, "  <text x='%d' y='%d' font-size='10' text-anchor='end'>%s</text>\n",
				left-5, y+barH/2+4, label)
		}
	}
}

func renderSVGColumns(b *strings.Builder, c Chart, ds Dataset, left, top, plotW, plotH int) {
	domain, span := chartDomain(c, ds)
	nLabels := len(ds.Labels)
	if nLabels == 0 {
		return
	}
	colW := plotW / nLabels

	for li := range ds.Labels {
		x := left + li*colW
		seriesW := colW / len(ds.Series)
		for si, s := range ds.Series {
			val := s.Points[li].Value
			colH := int(float64(plotH) * (val - domain.Min) / span)
			color := svgColor(s, si)
			fmt.Fprintf(b, "  <rect x='%d' y='%d' width='%d' height='%d' fill='%s' />\n",
				x+si*seriesW, top+plotH-colH, seriesW-1, colH, color)
		}
	}
}

func renderSVGLines(b *strings.Builder, c Chart, ds Dataset, left, top, plotW, plotH int) {
	domain, span := chartDomain(c, ds)
	nPoints := len(ds.Labels)
	if nPoints == 0 {
		return
	}

	for si, s := range ds.Series {
		var points []string
		for pi, p := range s.Points {
			x := left + int(float64(plotW)*float64(pi)/float64(nPoints-1))
			y := top + plotH - int(float64(plotH)*((p.Value-domain.Min)/span))
			points = append(points, fmt.Sprintf("%d,%d", x, y))
		}
		color := svgColor(s, si)
		fmt.Fprintf(b, "  <polyline points='%s' fill='none' stroke='%s' stroke-width='2' />\n",
			strings.Join(points, " "), color)
	}
}

func renderSVGScatter(b *strings.Builder, c Chart, ds Dataset, left, top, plotW, plotH int) {
	xMin, xMax := math.MaxFloat64, -math.MaxFloat64
	yMin, yMax := math.MaxFloat64, -math.MaxFloat64
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
		}
	}
	xSpan := xMax - xMin
	if xSpan == 0 {
		xSpan = 1
	}
	ySpan := yMax - yMin
	if ySpan == 0 {
		ySpan = 1
	}

	for si, s := range ds.Series {
		color := svgColor(s, si)
		for _, p := range s.Points {
			cx := left + int(float64(plotW)*((p.X-xMin)/xSpan))
			cy := top + plotH - int(float64(plotH)*((p.Value-yMin)/ySpan))
			fmt.Fprintf(b, "  <circle cx='%d' cy='%d' r='4' fill='%s' />\n",
				cx, cy, color)
		}
	}
}

func renderSVGPie(b *strings.Builder, c Chart, ds Dataset, left, top, plotW, plotH int) {
	if len(ds.Series) == 0 {
		return
	}
	s := ds.Series[0]

	total := 0.0
	for _, p := range s.Points {
		total += p.Value
	}
	if total <= 0 {
		return
	}

	cx := float64(left) + float64(plotW)/2
	cy := float64(top) + float64(plotH)/2
	r := math.Min(float64(plotW), float64(plotH))/2 - 2

	angle := -math.Pi / 2 // start at top
	for pi, p := range s.Points {
		sweep := (p.Value / total) * 2 * math.Pi
		x1 := cx + r*math.Cos(angle)
		y1 := cy + r*math.Sin(angle)
		x2 := cx + r*math.Cos(angle+sweep)
		y2 := cy + r*math.Sin(angle+sweep)

		largeArc := 0
		if sweep > math.Pi {
			largeArc = 1
		}

		color := p.Color
		if color == "" {
			color = svgPalette[pi%len(svgPalette)]
		}

		fmt.Fprintf(b, "  <path d='M%.1f,%.1f L%.1f,%.1f A%.1f,%.1f 0 %d,1 %.1f,%.1f Z' fill='%s' />\n",
			cx, cy, x1, y1, r, r, largeArc, x2, y2, color)

		angle += sweep
	}
}

func renderSVGHeatmap(b *strings.Builder, c Chart, ds Dataset, left, top, plotW, plotH int) {
	nRows := len(ds.Series)
	if nRows == 0 {
		return
	}
	nCols := 0
	for _, s := range ds.Series {
		if len(s.Points) > nCols {
			nCols = len(s.Points)
		}
	}
	if nCols == 0 {
		return
	}

	domain, span := chartDomain(c, ds)
	cellW := plotW / nCols
	cellH := plotH / nRows

	for ri, s := range ds.Series {
		for ci, p := range s.Points {
			idx := paletteIndex(p.Value, domain.Min, span)
			color := heatmapPalette[idx]

			x := left + ci*cellW
			y := top + ri*cellH
			fmt.Fprintf(b, "  <rect x='%d' y='%d' width='%d' height='%d' fill='%s' />\n",
				x, y, cellW-1, cellH-1, color)
		}

		// Row label
		if c.ShowYAxis {
			fmt.Fprintf(b, "  <text x='%d' y='%d' font-size='9' text-anchor='end'>%s</text>\n",
				left-3, top+ri*cellH+cellH/2+3, s.Name)
		}
	}
}

func renderSVGSparkline(b *strings.Builder, c Chart, ds Dataset, left, top, plotW, plotH int) {
	if len(ds.Series) == 0 {
		return
	}
	s := ds.Series[0]
	n := len(s.Points)
	if n == 0 {
		return
	}

	domain, span := chartDomain(c, ds)
	color := svgColor(s, 0)

	// Build polyline points + area path
	var linePoints []string
	var areaPath strings.Builder

	for i, p := range s.Points {
		x := left + int(float64(plotW)*float64(i)/float64(n-1))
		y := top + plotH - int(float64(plotH)*((p.Value-domain.Min)/span))
		linePoints = append(linePoints, fmt.Sprintf("%d,%d", x, y))
	}

	// Area fill: same points + close to bottom
	fmt.Fprintf(&areaPath, "M%s", linePoints[0])
	for _, pt := range linePoints[1:] {
		fmt.Fprintf(&areaPath, " L%s", pt)
	}
	fmt.Fprintf(&areaPath, " L%d,%d L%d,%d Z",
		left+plotW, top+plotH, left, top+plotH)

	fmt.Fprintf(b, "  <path d='%s' fill='%s' fill-opacity='0.2' stroke='none' />\n",
		areaPath.String(), color)
	fmt.Fprintf(b, "  <polyline points='%s' fill='none' stroke='%s' stroke-width='1.5' />\n",
		strings.Join(linePoints, " "), color)
}

func renderSVGLegend(b *strings.Builder, c Chart, ds Dataset, x, y int) {
	for si, s := range ds.Series {
		ly := y + 15 + si*18
		color := svgColor(s, si)
		fmt.Fprintf(b, "  <circle cx='%d' cy='%d' r='5' fill='%s' />\n",
			x+5, ly-4, color)
		fmt.Fprintf(b, "  <text x='%d' y='%d' font-size='11'>%s</text>\n",
			x+15, ly, s.Name)
	}
}
