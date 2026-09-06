package styles_test

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/tui/styles"
)

// ExampleNewCommon builds the shared layout context from a theme and
// renders a padded region; color escapes are omitted by using %q on a
// color-free style.
func ExampleNewCommon() {
	theme := cli.Theme{
		Palette: cli.Neon,
		Accent:  lipgloss.Color("#7ED957"),
		Muted:   lipgloss.Color("#6B7280"),
	}

	common := styles.NewCommon(theme, 80, 24)
	fmt.Println("content rows:", common.ContentHeight())
	fmt.Printf("%q\n", common.Styles.Main.Render("body"))
	// Output:
	// content rows: 22
	// "        \n  body  \n        "
}
