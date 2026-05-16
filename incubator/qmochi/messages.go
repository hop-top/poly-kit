package qmochi

// SetChartMsg is a Bubble Tea message to update the chart configuration.
type SetChartMsg struct {
	Chart Chart
}

// SetSizeMsg is a Bubble Tea message to update the chart size.
type SetSizeMsg struct {
	Size Size
}
