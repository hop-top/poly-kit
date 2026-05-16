package qmochi

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// ChartType defines the supported types of charts.
type ChartType string

const (
	// BarChart displays data with horizontal bars.
	BarChart ChartType = "bar"
	// ColumnChart displays data with vertical columns.
	ColumnChart ChartType = "column"
	// LineChart displays data as points connected by line segments.
	LineChart ChartType = "line"
	// SparklineChart displays data as a small, simple line chart without axes.
	SparklineChart ChartType = "sparkline"
	// HeatmapChart displays 2D data using color intensity.
	HeatmapChart ChartType = "heatmap"
	// BrailleChart displays high-resolution 2D data using Braille characters.
	BrailleChart ChartType = "braille"
	// PieChart displays data as slices of a circle.
	PieChart ChartType = "pie"
	// ScatterChart displays data points on an X/Y grid.
	ScatterChart ChartType = "scatter"
)

// BlockStyle defines the visual style of chart elements.
type BlockStyle string

const (
	// SolidBlock uses solid Unicode blocks (default).
	SolidBlock BlockStyle = "solid"
	// DottedBlock uses dotted Unicode characters.
	DottedBlock BlockStyle = "dotted"
	// DashedBlock uses dashed Unicode characters.
	DashedBlock BlockStyle = "dashed"
	// RoundedBlock uses rounded Unicode characters where applicable.
	RoundedBlock BlockStyle = "rounded"
	// ShadedBlock uses shaded Unicode characters (░, ▒, ▓).
	ShadedBlock BlockStyle = "shaded"
)

// Effect defines visual animations or special treatments.
type Effect string

const (
	// NoEffect uses standard rendering.
	NoEffect Effect = ""
	// BlinkEffect makes the element blink (if supported by terminal).
	BlinkEffect Effect = "blink"
)

// Point represents a single data point in a series.
// Value is the Y-axis value. X is optional (used by scatter).
type Point struct {
	Label  string
	X      float64
	Value  float64
	Color  string
	Effect Effect
}

// Series represents a named collection of data points with a specific color.
type Series struct {
	Name   string
	Points []Point
	Color  string
	Style  BlockStyle // overrides Chart.Style when set
	Effect Effect
}

// Size represents the dimensions of the chart in characters.
type Size struct {
	Width  int
	Height int
}

// Chart represents a chart configuration and its data.
type Chart struct {
	Type      ChartType
	Title     string
	Subtitle  string
	Series    []Series
	Size      Size
	Style     BlockStyle
	CellGlyph string   // override cell character (default "█")
	XLabels   []string // custom x-axis labels (sparse ok; empty slots skipped)
	Compact   bool     // pack two rows per terminal line via half-block chars
	DomainMin *float64 // override min value for scaling (default: 0 for bar/column)

	ShowXAxis  bool
	ShowYAxis  bool
	ShowLegend bool
	ShowValues bool
	NoColor    bool
}

// Dataset represents a normalized dataset ready for rendering.
type Dataset struct {
	Labels []string
	Series []Series
}

// writeHeader writes title and subtitle to b.
func writeHeader(b *strings.Builder, c Chart) {
	if c.Title != "" {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(c.Title))
		b.WriteString("\n")
	}
	if c.Subtitle != "" {
		b.WriteString(lipgloss.NewStyle().Italic(true).Render(c.Subtitle))
		b.WriteString("\n")
	}
}

// chartDomain returns the data domain for c, applying DomainMin
// override and zero-basing for bar/column charts.
func chartDomain(c Chart, ds Dataset) (Domain, float64) {
	d := DomainFor(ds)
	if c.DomainMin != nil {
		d.Min = *c.DomainMin
	} else if d.Min > 0 && (c.Type == BarChart || c.Type == ColumnChart) {
		d.Min = 0
	}
	span := d.Max - d.Min
	if span == 0 {
		span = 1
	}
	return d, span
}
