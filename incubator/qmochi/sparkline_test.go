package qmochi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderSparkline(t *testing.T) {
	series := Series{
		Points: []Point{
			{Value: 1},
			{Value: 3},
			{Value: 2},
			{Value: 5},
		},
	}

	output := RenderSparkline(series, 10)
	assert.Equal(t, 10, len([]rune(output)))
	assert.Contains(t, output, "█")
}
