package qmochi

import (
	"charm.land/lipgloss/v2"
)

// IntensityGlyphs maps 5 density levels to shading characters
// for NoColor heatmaps and similar intensity visualizations.
var IntensityGlyphs = []string{" ", "░", "▒", "▓", "█"}

// CategoryGlyphs provides distinct characters for NoColor
// categorical data (pie slices, multi-series differentiation).
var CategoryGlyphs = []string{"█", "▓", "▒", "░", "▚", "▞", "▖", "▗"}

// MarkerGlyphs provides distinct point markers for scatter plots.
var MarkerGlyphs = []string{"●", "○", "◆", "◇", "■", "□", "▲", "△"}

// ResolveStyle returns the series style if set, otherwise the chart style.
func ResolveStyle(chartStyle, seriesStyle BlockStyle) BlockStyle {
	if seriesStyle != "" {
		return seriesStyle
	}
	return chartStyle
}

// ApplyEffects applies visual effects to a Lip Gloss style.
func ApplyEffects(style lipgloss.Style, effect Effect) lipgloss.Style {
	switch effect {
	case BlinkEffect:
		return style.Blink(true)
	default:
		return style
	}
}

// GetPointStyle returns the lipgloss style for a specific point,
// merging point-level and series-level properties.
func GetPointStyle(p Point, s Series, noColor bool) lipgloss.Style {
	style := lipgloss.NewStyle()

	// Color
	color := p.Color
	if color == "" {
		color = s.Color
	}
	if !noColor && color != "" {
		style = style.Foreground(lipgloss.Color(color))
	}

	// Effect
	effect := p.Effect
	if effect == NoEffect {
		effect = s.Effect
	}
	style = ApplyEffects(style, effect)

	return style
}
