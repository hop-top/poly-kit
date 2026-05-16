package qmochi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderHeatmap(t *testing.T) {
	chart := Chart{
		Title: "Activity",
		Size:  Size{Width: 20, Height: 5},
		Series: []Series{
			{
				Name: "Mon",
				Points: []Point{
					{Label: "1", Value: 0},
					{Label: "2", Value: 5},
					{Label: "3", Value: 10},
				},
			},
		},
		NoColor:   true,
		ShowYAxis: true,
	}

	ds, _ := Normalize(chart)
	ly := LayoutFor(chart, ds)
	output := RenderHeatmap(chart, ds, ly)

	assert.Contains(t, output, "Activity")
	assert.Contains(t, output, "Mon")
	assert.Contains(t, output, "█")
}
