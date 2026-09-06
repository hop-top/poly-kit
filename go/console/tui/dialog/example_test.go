package dialog_test

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"hop.top/kit/go/console/tui/dialog"
)

// banner is a one-line Dialog that completes on its first message.
type banner struct {
	text string
	done bool
}

func (b banner) Update(tea.Msg) (dialog.Dialog, tea.Cmd) {
	b.done = true
	return b, nil
}

func (b banner) View(width, height int) string { return b.text }

func (b banner) Done() bool { return b.done }

// ExampleOverlay centers a dialog over base content, then routes a
// message to it and watches the completed dialog pop off the stack.
func ExampleOverlay() {
	base := "..........\n..........\n..........\n..........\n.........."

	ov := dialog.NewOverlay().Push(banner{text: "[ok]"})
	fmt.Println(ov.View(base, 10, 5))

	ov, _ = ov.Update(tea.KeyPressMsg{})
	fmt.Println(ov.IsActive())
	// Output:
	// ..........
	// ..........
	// ...[ok]...
	// ..........
	// ..........
	// false
}
