package qmochi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderLine(t *testing.T) {
	chart := Chart{
		Title: "Trend",
		Size:  Size{Width: 20, Height: 5},
		Series: []Series{
			{
				Name: "Data",
				Points: []Point{
					{Label: "A", Value: 1},
					{Label: "B", Value: 5},
					{Label: "C", Value: 2},
				},
			},
		},
		NoColor: true,
	}

	ds, _ := Normalize(chart)
	ly := LayoutFor(chart, ds)
	output, err := RenderLine(chart, ds, ly)

	require.NoError(t, err)
	assert.Contains(t, output, "Trend")
	assert.Contains(t, output, "•")
}

func TestRenderLine_MultipleSeriesError(t *testing.T) {
	chart := Chart{
		Series: []Series{{Name: "S1"}, {Name: "S2"}},
	}
	ds, _ := Normalize(chart)
	ly := LayoutFor(chart, ds)
	_, err := RenderLine(chart, ds, ly)
	assert.Error(t, err)
}
