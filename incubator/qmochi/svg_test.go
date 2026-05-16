package qmochi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderSVG_Bar(t *testing.T) {
	chart := Chart{
		Type: BarChart,
		Size: Size{Width: 200, Height: 100},
		Series: []Series{
			{Name: "A", Color: "#FF0000", Points: []Point{{Label: "X", Value: 10}}},
			{Name: "B", Color: "#00FF00", Points: []Point{{Label: "X", Value: 5}}},
		},
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)

	svg := RenderSVG(chart, ds)

	assert.Contains(t, svg, "<svg")
	assert.Contains(t, svg, "</svg>")
	assert.Contains(t, svg, "<rect")
	assert.Contains(t, svg, "#FF0000")
	assert.Contains(t, svg, "#00FF00")
}

func TestRenderSVG_Line(t *testing.T) {
	chart := Chart{
		Type: LineChart,
		Size: Size{Width: 200, Height: 100},
		Series: []Series{{
			Name:  "trend",
			Color: "#0000FF",
			Points: []Point{
				{Label: "A", Value: 10},
				{Label: "B", Value: 20},
				{Label: "C", Value: 15},
			},
		}},
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)

	svg := RenderSVG(chart, ds)

	assert.Contains(t, svg, "<svg")
	assert.Contains(t, svg, "<polyline")
	assert.Contains(t, svg, "#0000FF")
}

func TestRenderSVG_Scatter(t *testing.T) {
	chart := Chart{
		Type: ScatterChart,
		Size: Size{Width: 200, Height: 100},
		Series: []Series{
			{Name: "A", Color: "#FF0000", Points: []Point{{X: 1, Value: 2}}},
			{Name: "B", Color: "#00FF00", Points: []Point{{X: 3, Value: 4}}},
		},
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)

	svg := RenderSVG(chart, ds)

	assert.Contains(t, svg, "<svg")
	assert.Contains(t, svg, "<circle")
	assert.Contains(t, svg, "#FF0000")
}

func TestRenderSVG_Legend(t *testing.T) {
	chart := Chart{
		Type:       LineChart,
		Size:       Size{Width: 300, Height: 100},
		ShowLegend: true,
		Series: []Series{
			{Name: "day", Color: "#0000FF", Points: []Point{{Label: "A", Value: 1}}},
			{Name: "sales", Color: "#FF0000", Points: []Point{{Label: "A", Value: 2}}},
		},
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)

	svg := RenderSVG(chart, ds)

	assert.Contains(t, svg, "day")
	assert.Contains(t, svg, "sales")
}

func TestRenderSVG_Axes(t *testing.T) {
	chart := Chart{
		Type:      LineChart,
		Size:      Size{Width: 200, Height: 100},
		ShowXAxis: true,
		ShowYAxis: true,
		Series: []Series{{
			Name: "A",
			Points: []Point{
				{Label: "Jan", Value: 10},
				{Label: "Feb", Value: 20},
			},
		}},
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)

	svg := RenderSVG(chart, ds)

	assert.Contains(t, svg, "<line")
	assert.Contains(t, svg, "<text")
}

func TestRenderSVG_Pie(t *testing.T) {
	chart := Chart{
		Type: PieChart,
		Size: Size{Width: 200, Height: 200},
		Series: []Series{{
			Name: "Share",
			Points: []Point{
				{Label: "A", Value: 60, Color: "#FF0000"},
				{Label: "B", Value: 30, Color: "#00FF00"},
				{Label: "C", Value: 10, Color: "#0000FF"},
			},
		}},
		ShowLegend: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)

	svg := RenderSVG(chart, ds)

	assert.Contains(t, svg, "<svg")
	assert.Contains(t, svg, "<path")
	assert.Contains(t, svg, "#FF0000")
	assert.Contains(t, svg, "#00FF00")
	assert.Contains(t, svg, "A")
}

func TestRenderSVG_Heatmap(t *testing.T) {
	chart := Chart{
		Type: HeatmapChart,
		Size: Size{Width: 200, Height: 100},
		Series: []Series{
			{Name: "Mon", Points: []Point{
				{Label: "1", Value: 0}, {Label: "2", Value: 5}, {Label: "3", Value: 10},
			}},
			{Name: "Tue", Points: []Point{
				{Label: "1", Value: 3}, {Label: "2", Value: 8}, {Label: "3", Value: 2},
			}},
		},
		ShowYAxis: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)

	svg := RenderSVG(chart, ds)

	assert.Contains(t, svg, "<svg")
	assert.Contains(t, svg, "<rect")
	// Should have cells with different fill colors from heatmap palette
	assert.Contains(t, svg, "fill=")
}

func TestRenderSVG_Sparkline(t *testing.T) {
	chart := Chart{
		Type: SparklineChart,
		Size: Size{Width: 200, Height: 30},
		Series: []Series{{
			Name:  "trend",
			Color: "#50FA7B",
			Points: []Point{
				{Label: "1", Value: 1}, {Label: "2", Value: 4},
				{Label: "3", Value: 2}, {Label: "4", Value: 8},
			},
		}},
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)

	svg := RenderSVG(chart, ds)

	assert.Contains(t, svg, "<svg")
	assert.Contains(t, svg, "<polyline")
	assert.Contains(t, svg, "#50FA7B")
	// Sparkline SVG should have area fill
	assert.Contains(t, svg, "fill-opacity")
}

func TestRenderSVG_DefaultColors(t *testing.T) {
	chart := Chart{
		Type: BarChart,
		Size: Size{Width: 200, Height: 100},
		Series: []Series{
			{Name: "A", Points: []Point{{Label: "X", Value: 10}}},
			{Name: "B", Points: []Point{{Label: "X", Value: 5}}},
		},
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)

	svg := RenderSVG(chart, ds)

	// Should still produce valid SVG with default palette
	assert.Contains(t, svg, "<svg")
	assert.Contains(t, svg, "fill=")
	assert.True(t, strings.Count(svg, "fill=") >= 2,
		"each series should have a fill color")
}
