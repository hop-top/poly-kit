// Package markdown renders markdown to styled terminal output via glamour.
// Stateless functions only; no struct.
package markdown

import (
	"os"

	glamour "charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"github.com/charmbracelet/colorprofile"
)

// detectProfile reports the color profile of stdout. It is a variable so
// tests can pin a profile; production code always uses colorprofile.Detect,
// the same detection kit's help rendering uses (NO_COLOR, CLICOLOR_FORCE,
// TERM=dumb, stdout-is-not-a-terminal).
var detectProfile = func() colorprofile.Profile {
	return colorprofile.Detect(os.Stdout, os.Environ())
}

// plain reports whether output must be free of ANSI escapes: either the
// caller forced it (noColor) or stdout does not support color.
func plain(noColor bool) bool {
	return noColor || detectProfile() <= colorprofile.ASCII
}

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
//
// noColor true forces plain output (NoTTY/ASCII style, no ANSI escapes).
// noColor false means "auto": the hop.top-accented dark style is used only
// when stdout supports color; with NO_COLOR set, TERM=dumb, or stdout piped
// or redirected, output is plain, matching the rest of kit's output.
// CLICOLOR_FORCE=1 keeps styling on a non-terminal.
func Render(src string, noColor bool) (string, error) {
	if plain(noColor) {
		return glamour.Render(src, styles.NoTTYStyle)
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(defaultStyle()))
	if err != nil {
		return "", err
	}
	return r.Render(src)
}

// RenderWith renders src using a custom [ansi.StyleConfig].
// noColor true forces plain output and the custom style is ignored
// (NoTTY/ASCII). noColor false auto-detects as [Render] does: the custom
// style applies only when stdout supports color.
func RenderWith(src string, style ansi.StyleConfig, noColor bool) (string, error) {
	if plain(noColor) {
		return glamour.Render(src, styles.NoTTYStyle)
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(style))
	if err != nil {
		return "", err
	}
	return r.Render(src)
}
