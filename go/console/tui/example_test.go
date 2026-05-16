package tui_test

import (
	"fmt"

	"hop.top/kit/go/console/tui"
	"hop.top/kit/go/console/tui/dialog"
	"hop.top/kit/go/console/tui/styles"
)

// stubListItem is a minimal Item for the example.
type stubListItem struct{ text string }

func (s stubListItem) Render(width int) string {
	if width > 0 && len(s.text) > width {
		return s.text[:width]
	}
	return s.text
}

// ExampleLibrary demonstrates creating core TUI components from the kit/tui
// library: a List with stub items, an Overlay, and styles from a theme.
func Example() {
	theme := testTheme()

	// Create styles from a theme.
	s := styles.NewStyles(theme)
	_ = s.Accent // semantic styles are available

	common := styles.NewCommon(theme, 80, 24)
	fmt.Println("content height:", common.ContentHeight())

	// Create a List with stub items.
	items := []tui.Item{
		stubListItem{text: "deploy v2.1"},
		stubListItem{text: "rollback v2.0"},
		stubListItem{text: "scale replicas"},
	}
	list := tui.NewList(5).SetItems(items)
	fmt.Println("list items:", len(list.Items()))
	fmt.Println(list.View(40))

	// Create an Overlay (dialog stack).
	ov := dialog.NewOverlay()
	fmt.Println("overlay active:", ov.IsActive())

	// Output:
	// content height: 22
	// list items: 3
	// deploy v2.1
	// rollback v2.0
	// scale replicas
	// overlay active: false
}
