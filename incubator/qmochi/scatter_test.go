package qmochi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderScatter_Basic(t *testing.T) {
	chart := Chart{
		Type:  ScatterChart,
		Title: "Metrics",
		Size:  Size{Width: 30, Height: 10},
		Series: []Series{
			{
				Name: "day",
				Points: []Point{
					{X: 0, Value: 0},
					{X: 3, Value: 0.5},
					{X: 5, Value: 0.6},
				},
			},
			{
				Name: "sales",
				Points: []Point{
					{X: 2, Value: 0.15},
					{X: 4, Value: 0.9},
				},
			},
		},
		NoColor:   true,
		ShowYAxis: true,
		ShowXAxis: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderScatter(chart, ds, ly)

	assert.Contains(t, out, "Metrics")
	// Each series uses a distinct marker
	assert.Contains(t, out, MarkerGlyphs[0])
	assert.Contains(t, out, MarkerGlyphs[1])
}

func TestRenderScatter_DistinctMarkers(t *testing.T) {
	chart := Chart{
		Type: ScatterChart,
		Size: Size{Width: 20, Height: 8},
		Series: []Series{
			{Name: "A", Points: []Point{{X: 1, Value: 1}}},
			{Name: "B", Points: []Point{{X: 2, Value: 2}}},
			{Name: "C", Points: []Point{{X: 3, Value: 3}}},
			{Name: "D", Points: []Point{{X: 4, Value: 4}}},
		},
		NoColor: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderScatter(chart, ds, ly)

	// 4 series = 4 distinct markers
	for i := 0; i < 4; i++ {
		assert.Contains(t, out, MarkerGlyphs[i],
			"series %d marker %q missing", i, MarkerGlyphs[i])
	}
}

func TestRenderScatter_Legend(t *testing.T) {
	chart := Chart{
		Type: ScatterChart,
		Size: Size{Width: 20, Height: 8},
		Series: []Series{
			{Name: "day", Points: []Point{{X: 0, Value: 0}}},
			{Name: "sales", Points: []Point{{X: 1, Value: 1}}},
		},
		ShowLegend: true,
		NoColor:    true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderScatter(chart, ds, ly)

	assert.Contains(t, out, "day="+MarkerGlyphs[0])
	assert.Contains(t, out, "sales="+MarkerGlyphs[1])
}

func TestRenderScatter_WithColor(t *testing.T) {
	chart := Chart{
		Type: ScatterChart,
		Size: Size{Width: 20, Height: 8},
		Series: []Series{
			{Name: "A", Color: "#FF0000", Points: []Point{{X: 1, Value: 1}}},
			{Name: "B", Color: "#00FF00", Points: []Point{{X: 2, Value: 2}}},
		},
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderScatter(chart, ds, ly)

	// Should contain ANSI color codes
	assert.Contains(t, out, "\x1b[")
}

func TestRenderScatter_Empty(t *testing.T) {
	chart := Chart{
		Type:   ScatterChart,
		Size:   Size{Width: 20, Height: 8},
		Series: []Series{{Name: "A", Points: []Point{}}},
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderScatter(chart, ds, ly)

	// Should not panic, produces grid of spaces
	assert.NotEmpty(t, out)
	// No markers
	for _, m := range MarkerGlyphs {
		assert.NotContains(t, out, m)
	}
}

func TestPoint_XField(t *testing.T) {
	p := Point{X: 3.5, Value: 7.2, Label: "test"}
	assert.Equal(t, 3.5, p.X)
	assert.Equal(t, 7.2, p.Value)
}

func TestScatterChart_Type(t *testing.T) {
	assert.Equal(t, ChartType("scatter"), ScatterChart)
}

func TestMarkerGlyphs_Distinct(t *testing.T) {
	seen := make(map[string]bool)
	for _, g := range MarkerGlyphs {
		assert.False(t, seen[g], "duplicate marker glyph: %q", g)
		seen[g] = true
	}
	assert.GreaterOrEqual(t, len(MarkerGlyphs), 4,
		"need at least 4 distinct markers")
}
