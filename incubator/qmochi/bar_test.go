package qmochi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderBar(t *testing.T) {
	chart := Chart{
		Title: "Sales",
		Size:  Size{Width: 20, Height: 5},
		Series: []Series{
			{
				Name: "2026",
				Points: []Point{
					{Label: "Jan", Value: 3},
					{Label: "Feb", Value: 7},
					{Label: "Mar", Value: 5},
				},
			},
		},
		NoColor: true,
	}

	ds, _ := Normalize(chart)
	ly := LayoutFor(chart, ds)
	output := RenderBar(chart, ds, ly)

	assert.Contains(t, output, "Sales")
	assert.Contains(t, output, "Jan")
	assert.Contains(t, output, "Feb")
	assert.Contains(t, output, "Mar")
	assert.Contains(t, output, "█")
}
