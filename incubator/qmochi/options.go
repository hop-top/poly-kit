package qmochi

// RendererOptions provides additional configuration for chart renderers.
// This is currently a placeholder for future expansion as specific
// chart type rendering logic is implemented.
type RendererOptions struct {
	// Padding defines the whitespace around the chart area.
	Padding int

	// AxisColor defines the color used for axes and ticks.
	AxisColor string

	// LabelColor defines the color used for labels.
	LabelColor string
}
