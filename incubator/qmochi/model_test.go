package qmochi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChart_Validate(t *testing.T) {
	tests := []struct {
		name    string
		chart   Chart
		wantErr error
	}{
		{
			name: "valid chart",
			chart: Chart{
				Series: []Series{
					{
						Name: "Series 1",
						Points: []Point{
							{Label: "A", Value: 1},
							{Label: "B", Value: 2},
						},
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "empty series name",
			chart: Chart{
				Series: []Series{
					{
						Name: "",
						Points: []Point{
							{Label: "A", Value: 1},
						},
					},
				},
			},
			wantErr: ErrEmptySeriesName,
		},
		{
			name: "duplicate point label",
			chart: Chart{
				Series: []Series{
					{
						Name: "Series 1",
						Points: []Point{
							{Label: "A", Value: 1},
							{Label: "A", Value: 2},
						},
					},
				},
			},
			wantErr: ErrDuplicatePointLabel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.chart.Validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	chart := Chart{
		Series: []Series{
			{
				Name: "S1",
				Points: []Point{
					{Label: "A", Value: 10},
					{Label: "B", Value: 20},
				},
			},
			{
				Name: "S2",
				Points: []Point{
					{Label: "B", Value: 30},
					{Label: "C", Value: 40},
				},
			},
		},
	}

	dataset, err := Normalize(chart)
	require.NoError(t, err)

	// Check labels order (first-seen)
	assert.Equal(t, []string{"A", "B", "C"}, dataset.Labels)

	// Check S1
	require.Len(t, dataset.Series, 2)
	assert.Equal(t, "S1", dataset.Series[0].Name)
	assert.Equal(t, []Point{
		{Label: "A", Value: 10},
		{Label: "B", Value: 20},
		{Label: "C", Value: 0},
	}, dataset.Series[0].Points)

	// Check S2
	assert.Equal(t, "S2", dataset.Series[1].Name)
	assert.Equal(t, []Point{
		{Label: "A", Value: 0},
		{Label: "B", Value: 30},
		{Label: "C", Value: 40},
	}, dataset.Series[1].Points)
}

func TestNormalize_Empty(t *testing.T) {
	chart := Chart{}
	dataset, err := Normalize(chart)
	require.NoError(t, err)
	assert.Empty(t, dataset.Labels)
	assert.Empty(t, dataset.Series)
}
