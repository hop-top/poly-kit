// Package markdown renders markdown to styled terminal output via glamour.
// Stateless functions only; no struct.
package markdown

import (
	glamour "charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

// accent is the hop.top brand accent used for headings and links.
const accent = "#FFFFFF"

// defaultStyle returns DarkStyleConfig with hop.top accent on headings and links.
func defaultStyle() ansi.StyleConfig {
	s := styles.DarkStyleConfig
	a := stringPtr(accent)
	s.H1.Color = a
	s.H2.Color = a
	s.H3.Color = a
	s.H4.Color = a
	s.H5.Color = a
	s.H6.Color = a
	s.Link.Color = a
	return s
}

func stringPtr(s string) *string { return &s }

// Render renders src as styled terminal markdown.
// When noColor is true, output uses the NoTTY/ASCII style (no ANSI escapes).
// Otherwise the hop.top-accented dark style is used.
func Render(src string, noColor bool) (string, error) {
	if noColor {
		return glamour.Render(src, styles.NoTTYStyle)
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(defaultStyle()))
	if err != nil {
		return "", err
	}
	return r.Render(src)
}

// RenderWith renders src using a custom [ansi.StyleConfig].
// When noColor is true, the custom style is ignored and NoTTY/ASCII is used.
func RenderWith(src string, style ansi.StyleConfig, noColor bool) (string, error) {
	if noColor {
		return glamour.Render(src, styles.NoTTYStyle)
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(style))
	if err != nil {
		return "", err
	}
	return r.Render(src)
}
