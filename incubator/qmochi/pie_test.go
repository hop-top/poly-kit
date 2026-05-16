package qmochi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderPie(t *testing.T) {
	chart := Chart{
		Title: "Market Share",
		Size:  Size{Width: 20, Height: 10},
		Series: []Series{
			{
				Name: "Share",
				Points: []Point{
					{Label: "A", Value: 50},
					{Label: "B", Value: 50},
				},
			},
		},
		NoColor: true,
	}

	ds, _ := Normalize(chart)
	ly := LayoutFor(chart, ds)
	output, err := RenderPie(chart, ds, ly)

	assert.NoError(t, err)
	assert.Contains(t, output, "Market Share")
	assert.Contains(t, output, "█")
}
