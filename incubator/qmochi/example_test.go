package qmochi_test

import (
	"fmt"

	"hop.top/kit/incubator/qmochi"
)

func ExampleRenderBar() {
	chart := qmochi.Chart{
		Type:  qmochi.BarChart,
		Title: "Sales",
		Size:  qmochi.Size{Width: 20, Height: 5},
		Series: []qmochi.Series{
			{
				Name: "2026",
				Points: []qmochi.Point{
					{Label: "Jan", Value: 10},
					{Label: "Feb", Value: 20},
				},
			},
		},
		NoColor: true,
	}

	ds, _ := qmochi.Normalize(chart)
	ly := qmochi.LayoutFor(chart, ds)
	output := qmochi.RenderBar(chart, ds, ly)
	fmt.Println(output)
}

func ExampleRenderColumn() {
	chart := qmochi.Chart{
		Type:  qmochi.ColumnChart,
		Title: "Sales",
		Size:  qmochi.Size{Width: 20, Height: 5},
		Series: []qmochi.Series{
			{
				Name: "2026",
				Points: []qmochi.Point{
					{Label: "Jan", Value: 10},
					{Label: "Feb", Value: 20},
				},
			},
		},
		NoColor: true,
	}

	ds, _ := qmochi.Normalize(chart)
	ly := qmochi.LayoutFor(chart, ds)
	output := qmochi.RenderColumn(chart, ds, ly)
	fmt.Println(output)
}

func ExampleRenderLine() {
	chart := qmochi.Chart{
		Type:  qmochi.LineChart,
		Title: "Trend",
		Size:  qmochi.Size{Width: 20, Height: 5},
		Series: []qmochi.Series{
			{
				Name: "Data",
				Points: []qmochi.Point{
					{Label: "A", Value: 10},
					{Label: "B", Value: 20},
				},
			},
		},
		NoColor: true,
	}

	ds, _ := qmochi.Normalize(chart)
	ly := qmochi.LayoutFor(chart, ds)
	output, _ := qmochi.RenderLine(chart, ds, ly)
	fmt.Println(output)
}

func ExampleRenderSparkline() {
	series := qmochi.Series{
		Points: []qmochi.Point{
			{Value: 1},
			{Value: 3},
			{Value: 2},
			{Value: 5},
		},
	}

	output := qmochi.RenderSparkline(series, 10)
	fmt.Println(output)
}

func ExampleRenderLineBraille() {
	chart := qmochi.Chart{
		Type:  qmochi.BrailleChart,
		Title: "High-Res Trend",
		Size:  qmochi.Size{Width: 20, Height: 5},
		Series: []qmochi.Series{
			{
				Name: "Data",
				Points: []qmochi.Point{
					{Label: "A", Value: 10},
					{Label: "B", Value: 25},
					{Label: "C", Value: 15},
					{Label: "D", Value: 30},
				},
			},
		},
		NoColor: true,
	}

	ds, _ := qmochi.Normalize(chart)
	ly := qmochi.LayoutFor(chart, ds)
	output, _ := qmochi.RenderLineBraille(chart, ds, ly)
	fmt.Println(output)
}

func ExampleRenderHeatmap() {
	// Recreating a month of GitHub activity
	days := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	series := make([]qmochi.Series, 7)
	for i, day := range days {
		points := make([]qmochi.Point, 10)
		for j := 0; j < 10; j++ {
			// Fake activity values
			val := float64((i + j) % 15)
			effect := qmochi.NoEffect
			if val == 14 {
				effect = qmochi.BlinkEffect
			}
			points[j] = qmochi.Point{Label: fmt.Sprintf("%d", j), Value: val, Effect: effect}
		}
		series[i] = qmochi.Series{Name: day, Points: points}
	}

	chart := qmochi.Chart{
		Type:      qmochi.HeatmapChart,
		Title:     "GitHub Activity",
		Series:    series,
		ShowYAxis: true,
		NoColor:   true, // For deterministic example output
	}

	ds, _ := qmochi.Normalize(chart)
	ly := qmochi.LayoutFor(chart, ds)
	output := qmochi.RenderHeatmap(chart, ds, ly)
	fmt.Println(output)
}

func ExampleRenderPie() {
	chart := qmochi.Chart{
		Type:  qmochi.PieChart,
		Title: "Browser Market Share",
		Size:  qmochi.Size{Width: 20, Height: 10},
		Series: []qmochi.Series{
			{
				Name: "Market Share",
				Points: []qmochi.Point{
					{Label: "Chrome", Value: 65, Color: "#4285F4"},
					{Label: "Safari", Value: 18, Color: "#34A853"},
					{Label: "Firefox", Value: 3, Color: "#FBBC05"},
					{Label: "Edge", Value: 4, Color: "#EA4335"},
				},
			},
		},
		NoColor: true,
	}

	ds, _ := qmochi.Normalize(chart)
	ly := qmochi.LayoutFor(chart, ds)
	output, _ := qmochi.RenderPie(chart, ds, ly)
	fmt.Println(output)
}

func ExampleNewModel() {
	chart := qmochi.Chart{
		Type: qmochi.BarChart,
		Size: qmochi.Size{Width: 40, Height: 10},
	}
	m := qmochi.NewModel(chart)
	fmt.Printf("Model initialized with chart type: %s\n", chart.Type)
	_ = m
	// Output: Model initialized with chart type: bar
}
