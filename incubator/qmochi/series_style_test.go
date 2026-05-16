package qmochi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBar_PerSeriesStyle(t *testing.T) {
	chart := Chart{
		Type: BarChart,
		Size: Size{Width: 40, Height: 5},
		Series: []Series{
			{
				Name:  "day",
				Style: SolidBlock,
				Points: []Point{
					{Label: "A", Value: 10},
				},
			},
			{
				Name:  "sales",
				Style: ShadedBlock,
				Points: []Point{
					{Label: "A", Value: 8},
				},
			},
			{
				Name:  "costs",
				Style: DottedBlock,
				Points: []Point{
					{Label: "A", Value: 5},
				},
			},
		},
		NoColor: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderBar(chart, ds, ly)

	// Solid uses █, shaded uses ░▒▓, dotted uses ·
	assert.Contains(t, out, "█", "solid series missing")
	assert.True(t,
		strings.Contains(out, "░") || strings.Contains(out, "▒") || strings.Contains(out, "▓"),
		"shaded series missing")
	assert.Contains(t, out, "·", "dotted series missing")
}

func TestBar_SeriesStyleOverridesChart(t *testing.T) {
	chart := Chart{
		Type:  BarChart,
		Style: SolidBlock,
		Size:  Size{Width: 40, Height: 5},
		Series: []Series{
			{
				Name:  "A",
				Style: DottedBlock,
				Points: []Point{
					{Label: "X", Value: 10},
				},
			},
		},
		NoColor: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderBar(chart, ds, ly)

	// Series style should win over chart style
	assert.Contains(t, out, "·", "series style should override chart style")
}

func TestBar_SeriesStyleFallsBackToChart(t *testing.T) {
	chart := Chart{
		Type:  BarChart,
		Style: ShadedBlock,
		Size:  Size{Width: 40, Height: 5},
		Series: []Series{
			{
				Name: "A",
				// No Style set — should use chart's ShadedBlock
				Points: []Point{
					{Label: "X", Value: 10},
				},
			},
		},
		NoColor: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderBar(chart, ds, ly)

	assert.True(t,
		strings.Contains(out, "░") || strings.Contains(out, "▒") || strings.Contains(out, "▓"),
		"should fall back to chart style")
}

func TestColumn_PerSeriesStyle(t *testing.T) {
	chart := Chart{
		Type: ColumnChart,
		Size: Size{Width: 20, Height: 6},
		Series: []Series{
			{
				Name:  "A",
				Style: SolidBlock,
				Points: []Point{
					{Label: "X", Value: 10},
				},
			},
			{
				Name:  "B",
				Style: DottedBlock,
				Points: []Point{
					{Label: "X", Value: 8},
				},
			},
		},
		NoColor: true,
	}

	ds, err := Normalize(chart)
	require.NoError(t, err)
	ly := LayoutFor(chart, ds)
	out := RenderColumn(chart, ds, ly)

	assert.Contains(t, out, "█", "solid series missing")
	assert.Contains(t, out, "·", "dotted series missing")
}
