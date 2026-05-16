package qmochi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLayoutFor(t *testing.T) {
	chart := Chart{
		Size:      Size{Width: 40, Height: 10},
		Title:     "Test Chart",
		ShowXAxis: true,
		ShowYAxis: true,
	}

	tests := []struct {
		name     string
		dataset  Dataset
		wantZero int
	}{
		{
			name: "positive-only",
			dataset: Dataset{
				Series: []Series{{Points: []Point{{Value: 10}, {Value: 20}}}},
			},
			wantZero: 8,
		},
		{
			name: "negative-only",
			dataset: Dataset{
				Series: []Series{{Points: []Point{{Value: -10}, {Value: -20}}}},
			},
			wantZero: 0,
		},
		{
			name: "mixed-sign",
			dataset: Dataset{
				Series: []Series{{Points: []Point{{Value: -10}, {Value: 10}}}},
			},
			wantZero: 4, // middle of 8 is 4
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := LayoutFor(chart, tt.dataset)
			assert.Equal(t, tt.wantZero, layout.ZeroRow)
			assert.Greater(t, layout.Plot.Width, 0)
			assert.Greater(t, layout.Plot.Height, 0)
		})
	}
}

func TestLayoutFor_ZeroSize(t *testing.T) {
	chart := Chart{Size: Size{Width: 0, Height: 0}}
	layout := LayoutFor(chart, Dataset{})
	assert.Equal(t, 0, layout.Plot.Width)
	assert.Equal(t, 0, layout.Plot.Height)
}
