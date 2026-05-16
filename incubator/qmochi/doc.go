// Package qmochi provides a lightweight, high-performance terminal charting
// library for Go, specifically designed to integrate seamlessly with
// Bubble Tea (charm.land/bubbletea) applications.
//
// # Core Concepts
//
// qmochi is built around three main phases:
//
//  1. Configuration (Chart): Define your data, chart type, and visual style.
//  2. Normalization (Dataset): Automatically align multiple series and fill gaps.
//  3. Rendering: Transform the dataset into a Lip Gloss-styled string or a
//     stateful Bubble Tea Model.
//
// # Features
//
//   - Multiple Chart Types: Bar, Column, Line, Sparkline, Heatmap, and Braille.
//   - High Resolution: Utilizes Unicode fractional blocks and Braille characters
//     to maximize data density in restricted terminal space.
//   - Custom Styles: Support for solid, dotted, dashed, and shaded textures.
//   - Bubble Tea Ready: Built-in Model that handles window resizing and
//     responsive layout.
//   - Zero Dependencies: Only depends on the Charm stack (bubbletea, lipgloss).
//
// # Getting Started
//
// Define a Chart, normalize the data, and render it:
//
//	chart := qmochi.Chart{
//	    Type:  qmochi.BarChart,
//	    Title: "CPU Usage",
//	    Series: []qmochi.Series{
//	        {Name: "User", Points: []qmochi.Point{{Label: "t1", Value: 40}}},
//	    },
//	}
//
//	ds, _ := qmochi.Normalize(chart)
//	ly := qmochi.LayoutFor(chart, ds)
//	output := qmochi.RenderBar(chart, ds, ly)
//
// For more detailed usage and examples, see the README.md and example_test.go files.
package qmochi
