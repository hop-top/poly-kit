package qmochi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"hop.top/kit/go/ai/llm"
)

func TestAuto_SingleSeriesFewLabels_Bar(t *testing.T) {
	data := []Series{{
		Name: "Revenue",
		Points: []Point{
			{Label: "Q1", Value: 100},
			{Label: "Q2", Value: 150},
			{Label: "Q3", Value: 120},
		},
	}}

	chart := Auto(data)

	assert.Equal(t, BarChart, chart.Type)
	assert.Equal(t, data, chart.Series)
}

func TestAuto_SingleSeriesManyLabels_Line(t *testing.T) {
	pts := make([]Point, 15)
	for i := range pts {
		pts[i] = Point{Label: "d" + string(rune('A'+i)), Value: float64(i * 3)}
	}
	data := []Series{{Name: "Trend", Points: pts}}

	chart := Auto(data)

	assert.Equal(t, LineChart, chart.Type)
}

func TestAuto_SingleSeriesProportional_Pie(t *testing.T) {
	data := []Series{{
		Name: "Share",
		Points: []Point{
			{Label: "Chrome", Value: 65},
			{Label: "Safari", Value: 18},
			{Label: "Firefox", Value: 7},
			{Label: "Other", Value: 10},
		},
	}}

	chart := Auto(data)

	assert.Equal(t, PieChart, chart.Type)
}

func TestAuto_SingleSeriesNoLabels_Sparkline(t *testing.T) {
	pts := make([]Point, 25)
	for i := range pts {
		pts[i] = Point{Value: float64(i % 7)}
	}
	data := []Series{{Name: "Metric", Points: pts}}

	chart := Auto(data)

	assert.Equal(t, SparklineChart, chart.Type)
}

func TestAuto_MultiSeriesFewLabels_Column(t *testing.T) {
	data := []Series{
		{Name: "2025", Points: []Point{
			{Label: "Q1", Value: 100}, {Label: "Q2", Value: 150},
		}},
		{Name: "2026", Points: []Point{
			{Label: "Q1", Value: 120}, {Label: "Q2", Value: 180},
		}},
	}

	chart := Auto(data)

	assert.Equal(t, ColumnChart, chart.Type)
}

func TestAuto_MultiSeriesManyLabels_Line(t *testing.T) {
	pts1 := make([]Point, 12)
	pts2 := make([]Point, 12)
	for i := range pts1 {
		pts1[i] = Point{Label: "m" + string(rune('A'+i)), Value: float64(i)}
		pts2[i] = Point{Label: "m" + string(rune('A'+i)), Value: float64(i * 2)}
	}
	data := []Series{
		{Name: "A", Points: pts1},
		{Name: "B", Points: pts2},
	}

	chart := Auto(data)

	assert.Equal(t, LineChart, chart.Type)
}

func TestAuto_XValuesSet_Scatter(t *testing.T) {
	data := []Series{{
		Name: "Measurements",
		Points: []Point{
			{X: 1.5, Value: 3.2},
			{X: 2.7, Value: 4.1},
			{X: 4.0, Value: 2.8},
		},
	}}

	chart := Auto(data)

	assert.Equal(t, ScatterChart, chart.Type)
}

func TestAuto_GridShape_Heatmap(t *testing.T) {
	data := []Series{
		{Name: "Mon", Points: []Point{
			{Label: "W1", Value: 3}, {Label: "W2", Value: 7}, {Label: "W3", Value: 1},
		}},
		{Name: "Tue", Points: []Point{
			{Label: "W1", Value: 5}, {Label: "W2", Value: 2}, {Label: "W3", Value: 8},
		}},
		{Name: "Wed", Points: []Point{
			{Label: "W1", Value: 1}, {Label: "W2", Value: 9}, {Label: "W3", Value: 4},
		}},
		{Name: "Thu", Points: []Point{
			{Label: "W1", Value: 6}, {Label: "W2", Value: 3}, {Label: "W3", Value: 7},
		}},
		{Name: "Fri", Points: []Point{
			{Label: "W1", Value: 2}, {Label: "W2", Value: 5}, {Label: "W3", Value: 9},
		}},
	}

	chart := Auto(data)

	assert.Equal(t, HeatmapChart, chart.Type)
}

func TestAuto_Empty(t *testing.T) {
	chart := Auto(nil)
	assert.Equal(t, BarChart, chart.Type, "empty data defaults to bar")
}

func TestAuto_SinglePoint(t *testing.T) {
	data := []Series{{
		Name:   "Single",
		Points: []Point{{Label: "X", Value: 42}},
	}}

	chart := Auto(data)

	assert.Equal(t, BarChart, chart.Type)
}

// fakeCompleter returns a fixed chart type string.
type fakeCompleter struct {
	response string
}

func (f *fakeCompleter) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{Content: f.response}, nil
}

func TestAutoWithLLM_UsesLLMResponse(t *testing.T) {
	// Data is ambiguous (3 points, could be bar or pie)
	data := []Series{{
		Name: "Votes",
		Points: []Point{
			{Label: "A", Value: 40},
			{Label: "B", Value: 35},
			{Label: "C", Value: 25},
		},
	}}

	comp := &fakeCompleter{response: "pie"}
	chart, err := AutoWithLLM(context.Background(), data, comp)

	assert.NoError(t, err)
	assert.Equal(t, PieChart, chart.Type)
	assert.Equal(t, data, chart.Series)
}

func TestAutoWithLLM_FallsBackOnInvalid(t *testing.T) {
	data := []Series{{
		Name:   "Test",
		Points: []Point{{Label: "A", Value: 10}},
	}}

	comp := &fakeCompleter{response: "nonsense"}
	chart, err := AutoWithLLM(context.Background(), data, comp)

	assert.NoError(t, err)
	// Should fall back to deterministic Auto
	assert.Equal(t, Auto(data).Type, chart.Type)
}

func TestAutoWithLLM_ParsesFromSentence(t *testing.T) {
	data := []Series{{
		Name: "Data",
		Points: []Point{
			{Label: "X", Value: 5}, {Label: "Y", Value: 10},
		},
	}}

	comp := &fakeCompleter{response: "I recommend a scatter chart for this data"}
	chart, err := AutoWithLLM(context.Background(), data, comp)

	assert.NoError(t, err)
	assert.Equal(t, ScatterChart, chart.Type)
}

func TestAutoWithLLM_NilCompleter(t *testing.T) {
	data := []Series{{
		Name:   "Test",
		Points: []Point{{Label: "A", Value: 10}},
	}}

	chart, err := AutoWithLLM(context.Background(), data, nil)

	assert.NoError(t, err)
	// Falls back to deterministic Auto
	assert.Equal(t, Auto(data).Type, chart.Type)
}

func TestAuto_PreservesSeriesData(t *testing.T) {
	data := []Series{{
		Name:  "Test",
		Color: "#FF0000",
		Style: DottedBlock,
		Points: []Point{
			{Label: "A", Value: 1},
			{Label: "B", Value: 2},
		},
	}}

	chart := Auto(data)

	assert.Equal(t, data, chart.Series)
}
