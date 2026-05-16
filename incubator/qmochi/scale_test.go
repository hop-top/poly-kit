package qmochi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDomainFor(t *testing.T) {
	tests := []struct {
		name    string
		dataset Dataset
		want    Domain
	}{
		{
			name:    "empty dataset",
			dataset: Dataset{},
			want:    Domain{0, 0},
		},
		{
			name: "single point",
			dataset: Dataset{
				Series: []Series{
					{Points: []Point{{Value: 10}}},
				},
			},
			want: Domain{10, 10},
		},
		{
			name: "multiple series",
			dataset: Dataset{
				Series: []Series{
					{Points: []Point{{Value: -5}, {Value: 10}}},
					{Points: []Point{{Value: 0}, {Value: 25}}},
				},
			},
			want: Domain{-5, 25},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DomainFor(tt.dataset)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNiceTicks(t *testing.T) {
	tests := []struct {
		name     string
		domain   Domain
		maxTicks int
		wantMin  float64
		wantMax  float64
	}{
		{
			name:     "0 to 100",
			domain:   Domain{0, 100},
			maxTicks: 5,
			wantMin:  0,
			wantMax:  100,
		},
		{
			name:     "small range",
			domain:   Domain{0, 1},
			maxTicks: 5,
			wantMin:  0,
			wantMax:  1,
		},
		{
			name:     "negative range",
			domain:   Domain{-100, -10},
			maxTicks: 5,
			wantMin:  -100,
			wantMax:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ticks := NiceTicks(tt.domain, tt.maxTicks)
			assert.NotEmpty(t, ticks)
			assert.LessOrEqual(t, len(ticks), tt.maxTicks+2) // allow some flexibility
			assert.LessOrEqual(t, ticks[0].Value, tt.domain.Min)
			assert.GreaterOrEqual(t, ticks[len(ticks)-1].Value, tt.domain.Max)
		})
	}
}
