package qmochi

import (
	tea "charm.land/bubbletea/v2"
)

// Model represents a Bubble Tea model for rendering charts.
type Model struct {
	chart Chart
}

// NewModel creates a new Model with the given chart configuration.
func NewModel(chart Chart) Model {
	return Model{
		chart: chart,
	}
}

// Init initializes the Bubble Tea model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles Bubble Tea messages and updates the model state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.chart.Size = Size{Width: msg.Width, Height: msg.Height}
	case SetChartMsg:
		m.chart = msg.Chart
	case SetSizeMsg:
		m.chart.Size = msg.Size
	}
	return m, nil
}

// View renders the chart as a string.
func (m Model) View() tea.View {
	if m.chart.Size.Width <= 0 || m.chart.Size.Height <= 0 {
		return tea.NewView("")
	}

	ds, err := Normalize(m.chart)
	if err != nil {
		return tea.NewView("Error: " + err.Error())
	}

	ly := LayoutFor(m.chart, ds)

	var output string
	switch m.chart.Type {
	case BarChart:
		output = RenderBar(m.chart, ds, ly)
	case ColumnChart:
		output = RenderColumn(m.chart, ds, ly)
	case LineChart:
		var err error
		output, err = RenderLine(m.chart, ds, ly)
		if err != nil {
			return tea.NewView("Error: " + err.Error())
		}
	case SparklineChart:
		// Sparkline usually ignores layout and just uses width
		if len(ds.Series) > 0 {
			output = RenderSparkline(ds.Series[0], m.chart.Size.Width)
		}
	case HeatmapChart:
		output = RenderHeatmap(m.chart, ds, ly)
	case BrailleChart:
		var err error
		output, err = RenderLineBraille(m.chart, ds, ly)
		if err != nil {
			return tea.NewView("Error: " + err.Error())
		}
	case PieChart:
		var err error
		output, err = RenderPie(m.chart, ds, ly)
		if err != nil {
			return tea.NewView("Error: " + err.Error())
		}
	default:
		output = "Unknown chart type"
	}

	return tea.NewView(output)
}
