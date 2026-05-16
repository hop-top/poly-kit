package qmochi

import (
	"context"
	"fmt"
	"strings"

	"hop.top/kit/go/ai/llm"
)

// Auto inspects the shape of data and returns a Chart with
// the best-fitting chart type. Heuristics:
//
//   - X values set on any point → Scatter
//   - 1 series, >20 points, no labels → Sparkline
//   - 1 series, ≤6 points, values sum-proportional → Pie
//   - ≥3 series, uniform point count, ≥3 cols → Heatmap
//   - Multi-series, ≤8 labels → Column (grouped)
//   - Multi-series, >8 labels → Line (multi)
//   - 1 series, >8 labels → Line
//   - Default → Bar
func Auto(data []Series) Chart {
	chart := Chart{Series: data}

	if len(data) == 0 {
		chart.Type = BarChart
		return chart
	}

	// Check for X values → Scatter
	if hasXValues(data) {
		chart.Type = ScatterChart
		return chart
	}

	nSeries := len(data)
	nLabels := maxPointCount(data)

	if nSeries == 1 {
		return autoSingle(chart, data[0], nLabels)
	}

	return autoMulti(chart, data, nLabels)
}

func autoSingle(chart Chart, s Series, nLabels int) Chart {
	// Many points without labels → sparkline
	if nLabels > 20 && !hasLabels(s) {
		chart.Type = SparklineChart
		return chart
	}

	// Few categorical points that look proportional → pie
	if nLabels <= 6 && nLabels >= 2 && looksProportional(s) {
		chart.Type = PieChart
		return chart
	}

	// Many points → line
	if nLabels > 8 {
		chart.Type = LineChart
		return chart
	}

	// Default single series → bar
	chart.Type = BarChart
	return chart
}

func autoMulti(chart Chart, data []Series, nLabels int) Chart {
	// Grid shape (≥3 series, uniform cols, ≥3 cols) → heatmap
	if len(data) >= 3 && nLabels >= 3 && uniformPointCount(data) {
		chart.Type = HeatmapChart
		return chart
	}

	// Many labels → line (multi-series)
	if nLabels > 8 {
		chart.Type = LineChart
		return chart
	}

	// Few labels → column (grouped)
	chart.Type = ColumnChart
	return chart
}

// hasXValues checks if any point has a non-zero X value.
func hasXValues(data []Series) bool {
	for _, s := range data {
		for _, p := range s.Points {
			if p.X != 0 {
				return true
			}
		}
	}
	return false
}

// hasLabels checks if any point has a non-empty label.
func hasLabels(s Series) bool {
	for _, p := range s.Points {
		if p.Label != "" {
			return true
		}
	}
	return false
}

// maxPointCount returns the maximum number of points across
// all series.
func maxPointCount(data []Series) int {
	max := 0
	for _, s := range data {
		if len(s.Points) > max {
			max = len(s.Points)
		}
	}
	return max
}

// uniformPointCount checks if all series have the same number
// of points.
func uniformPointCount(data []Series) bool {
	if len(data) == 0 {
		return true
	}
	n := len(data[0].Points)
	for _, s := range data[1:] {
		if len(s.Points) != n {
			return false
		}
	}
	return true
}

// AutoWithLLM uses an LLM to pick the best chart type for
// ambiguous data. Falls back to deterministic Auto if the
// completer is nil or returns an unrecognized type.
func AutoWithLLM(ctx context.Context, data []Series, comp llm.Completer) (Chart, error) {
	if comp == nil {
		return Auto(data), nil
	}

	summary := summarizeData(data)
	resp, err := comp.Complete(ctx, llm.Request{
		Messages: []llm.Message{{
			Role: "user",
			Content: fmt.Sprintf(
				"Given this dataset, respond with exactly one word — the best "+
					"chart type: bar, column, line, sparkline, heatmap, braille, "+
					"scatter, or pie.\n\n%s", summary),
		}},
		Temperature: 0,
		MaxTokens:   20,
	})
	if err != nil {
		return Auto(data), nil
	}

	chartType := parseChartType(resp.Content)
	if chartType == "" {
		return Auto(data), nil
	}

	chart := Chart{
		Type:   chartType,
		Series: data,
	}
	return chart, nil
}

// parseChartType extracts a valid ChartType from LLM response.
func parseChartType(response string) ChartType {
	lower := strings.ToLower(strings.TrimSpace(response))
	types := map[string]ChartType{
		"bar":       BarChart,
		"column":    ColumnChart,
		"line":      LineChart,
		"sparkline": SparklineChart,
		"heatmap":   HeatmapChart,
		"braille":   BrailleChart,
		"scatter":   ScatterChart,
		"pie":       PieChart,
	}

	// Try exact match first
	if ct, ok := types[lower]; ok {
		return ct
	}

	// Try to find a chart type keyword in the response
	for keyword, ct := range types {
		if strings.Contains(lower, keyword) {
			return ct
		}
	}

	return ""
}

// summarizeData builds a compact text summary of the dataset
// for the LLM prompt.
func summarizeData(data []Series) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d series", len(data))

	for _, s := range data {
		fmt.Fprintf(&b, "\n- %q: %d points", s.Name, len(s.Points))
		if len(s.Points) > 0 {
			hasX := false
			hasLabel := false
			for _, p := range s.Points {
				if p.X != 0 {
					hasX = true
				}
				if p.Label != "" {
					hasLabel = true
				}
			}
			if hasX {
				b.WriteString(" [has X values]")
			}
			if hasLabel {
				fmt.Fprintf(&b, " labels: %s..%s",
					s.Points[0].Label,
					s.Points[len(s.Points)-1].Label)
			}
			fmt.Fprintf(&b, " range: %.2f..%.2f",
				minVal(s.Points), maxVal(s.Points))
		}
	}
	return b.String()
}

func minVal(pts []Point) float64 {
	m := pts[0].Value
	for _, p := range pts[1:] {
		if p.Value < m {
			m = p.Value
		}
	}
	return m
}

func maxVal(pts []Point) float64 {
	m := pts[0].Value
	for _, p := range pts[1:] {
		if p.Value > m {
			m = p.Value
		}
	}
	return m
}

// looksProportional checks if values look like parts of a
// whole: all positive, no single value >90% of total, and
// total is near 100 or near 1.0 (percentage/fraction).
func looksProportional(s Series) bool {
	if len(s.Points) < 2 {
		return false
	}
	total := 0.0
	max := 0.0
	for _, p := range s.Points {
		if p.Value < 0 {
			return false
		}
		total += p.Value
		if p.Value > max {
			max = p.Value
		}
	}
	if total == 0 {
		return false
	}
	if max/total > 0.90 {
		return false
	}
	// Values sum to ~100 (percentages) or ~1.0 (fractions)
	sumsToHundred := total >= 95 && total <= 105
	sumsToOne := total >= 0.95 && total <= 1.05
	return sumsToHundred || sumsToOne
}
