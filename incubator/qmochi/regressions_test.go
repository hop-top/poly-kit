package qmochi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bar: labels should be aligned (padded to longest label).
func TestRegression_BarLabelAlignment(t *testing.T) {
	chart := Chart{
		Type: BarChart,
		Size: Size{Width: 40, Height: 5},
		Series: []Series{{
			Name: "S",
			Points: []Point{
				{Label: "A", Value: 10},
				{Label: "Long Label", Value: 20},
				{Label: "B", Value: 15},
			},
		}},
		NoColor: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderBar(chart, ds, ly)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	// All data lines should have bars starting at the same column
	barStarts := make([]int, 0)
	for _, line := range lines {
		idx := strings.IndexAny(line, "█▏▎▍▌▋▊▉░▒▓")
		if idx >= 0 {
			barStarts = append(barStarts, idx)
		}
	}
	require.NotEmpty(t, barStarts)
	for i := 1; i < len(barStarts); i++ {
		assert.Equal(t, barStarts[0], barStarts[i],
			"bar start column mismatch: line 0 at %d, line %d at %d",
			barStarts[0], i, barStarts[i])
	}
}

// Bar: zero-based scaling (Bugs=12 should still show a bar).
func TestRegression_BarZeroBased(t *testing.T) {
	chart := Chart{
		Type: BarChart,
		Size: Size{Width: 40, Height: 5},
		Series: []Series{{
			Name: "S",
			Points: []Point{
				{Label: "High", Value: 45},
				{Label: "Low", Value: 12},
			},
		}},
		NoColor: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderBar(chart, ds, ly)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	// Both lines should contain bar glyphs
	for _, line := range lines {
		assert.True(t, strings.ContainsAny(line, "█▏▎▍▌▋▊▉░▒▓"),
			"expected bar glyphs in: %q", line)
	}
}

// Bar: DomainMin override.
func TestRegression_BarDomainMin(t *testing.T) {
	min := 10.0
	chart := Chart{
		Type:      BarChart,
		DomainMin: &min,
		Size:      Size{Width: 40, Height: 5},
		Series: []Series{{
			Name: "S",
			Points: []Point{
				{Label: "A", Value: 10},
				{Label: "B", Value: 20},
			},
		}},
		NoColor: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderBar(chart, ds, ly)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	// A=10 with min=10 should have zero-length bar (no glyphs)
	assert.False(t, strings.ContainsAny(lines[0], "█▏▎▍▌▋▊▉░▒▓"),
		"A=10 with DomainMin=10 should be empty")
	// B=20 should have a bar
	assert.True(t, strings.ContainsAny(lines[1], "█▏▎▍▌▋▊▉░▒▓"),
		"B=20 should have a bar")
}

// Column: zero-based scaling.
func TestRegression_ColumnZeroBased(t *testing.T) {
	chart := Chart{
		Type: ColumnChart,
		Size: Size{Width: 20, Height: 6},
		Series: []Series{{
			Name: "S",
			Points: []Point{
				{Label: "A", Value: 100},
				{Label: "B", Value: 50},
			},
		}},
		NoColor: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderColumn(chart, ds, ly)

	// B=50 is half of A=100; both should have visible columns
	assert.True(t, strings.ContainsAny(out, "█▂▃▄▅▆▇·"),
		"column chart should contain column glyphs")
}

// Heatmap NoColor: cells use shading glyphs, not uniform blocks.
func TestRegression_HeatmapNoColorShading(t *testing.T) {
	chart := Chart{
		Type: HeatmapChart,
		Series: []Series{
			{
				Name: "R",
				Points: []Point{
					{Label: "1", Value: 0},
					{Label: "2", Value: 5},
					{Label: "3", Value: 10},
					{Label: "4", Value: 15},
					{Label: "5", Value: 20},
				},
			},
		},
		NoColor:   true,
		ShowYAxis: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderHeatmap(chart, ds, ly)

	// Should contain multiple shading levels
	assert.Contains(t, out, " ", "level 0 should be space")
	assert.Contains(t, out, "░", "should contain light shade")
	assert.Contains(t, out, "█", "should contain full block")
}

// Pie NoColor: slices use distinct glyphs.
// Normalize must preserve Series.Style and Series.Effect.
func TestRegression_NormalizePreservesSeriesFields(t *testing.T) {
	chart := Chart{
		Type: BarChart,
		Series: []Series{
			{
				Name:   "A",
				Style:  DottedBlock,
				Effect: BlinkEffect,
				Color:  "#FF0000",
				Points: []Point{{Label: "X", Value: 1}},
			},
		},
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	require.Len(t, ds.Series, 1)

	assert.Equal(t, DottedBlock, ds.Series[0].Style, "Style lost in Normalize")
	assert.Equal(t, BlinkEffect, ds.Series[0].Effect, "Effect lost in Normalize")
	assert.Equal(t, "#FF0000", ds.Series[0].Color, "Color lost in Normalize")
}

// NiceTicks should not produce FP noise labels like 0.6000000000000001.
func TestRegression_NiceTicksNoFPNoise(t *testing.T) {
	ticks := NiceTicks(Domain{Min: 0, Max: 1}, 6)

	for _, tick := range ticks {
		assert.LessOrEqual(t, len(tick.Label), 5,
			"tick label too long (FP noise?): %q", tick.Label)
		assert.NotContains(t, tick.Label, "000000",
			"FP noise in tick label: %q", tick.Label)
	}
}

// Scatter chart should skip label dedup validation.
func TestRegression_ScatterSkipsLabelDedup(t *testing.T) {
	chart := Chart{
		Type: ScatterChart,
		Series: []Series{
			{
				Name: "A",
				Points: []Point{
					{X: 1, Value: 1},
					{X: 2, Value: 2},
					{X: 3, Value: 3},
				},
			},
		},
	}

	_, err := Normalize(chart)
	assert.NoError(t, err, "scatter should not fail on empty/duplicate labels")
}

// ResolveStyle returns series style when set, chart style otherwise.
func TestRegression_ResolveStyle(t *testing.T) {
	assert.Equal(t, DottedBlock, ResolveStyle(SolidBlock, DottedBlock))
	assert.Equal(t, SolidBlock, ResolveStyle(SolidBlock, ""))
	assert.Equal(t, BlockStyle(""), ResolveStyle("", ""))
}

func TestRegression_PieNoColorDistinctSlices(t *testing.T) {
	chart := Chart{
		Type: PieChart,
		Size: Size{Width: 20, Height: 10},
		Series: []Series{{
			Name: "S",
			Points: []Point{
				{Label: "A", Value: 50},
				{Label: "B", Value: 50},
			},
		}},
		NoColor: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out, err := RenderPie(chart, ds, ly)
	require.NoError(t, err)

	// Two 50/50 slices should use two different glyphs
	assert.Contains(t, out, "█", "slice A should use █")
	assert.Contains(t, out, "▓", "slice B should use ▓")
}
