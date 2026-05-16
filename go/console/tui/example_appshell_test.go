package tui_test

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"hop.top/kit/go/console/tui"
)

// todoApp is the canonical AppShell consumer: a tiny "header / main /
// footer" todo list that exercises every optional AppRenderer
// capability (Initer, Updater, Resizer, HeaderRenderer, FooterRenderer).
//
// The shape is intentionally small — adopters migrating from a hand-
// rolled tea.Model can scan it in one read.
type todoApp struct {
	items  []string
	cursor int
	width  int
	height int
}

// Render is the only required method; everything else is optional.
func (t todoApp) Render(width, height int) string {
	if len(t.items) == 0 {
		return "(no items)"
	}
	var b strings.Builder
	for i, item := range t.items {
		marker := "  "
		if i == t.cursor {
			marker = "> "
		}
		b.WriteString(marker)
		b.WriteString(item)
		if i < len(t.items)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Init seeds initial commands. Returning nil is fine if there's nothing
// to schedule.
func (t todoApp) Init() tea.Cmd {
	return nil
}

// Update handles app-specific keys. The shell already filtered q/esc/
// ctrl+c and ?/h before this runs, so the renderer never sees them.
func (t todoApp) Update(msg tea.Msg) (tui.AppRenderer, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "j", "down":
			if t.cursor < len(t.items)-1 {
				t.cursor++
			}
		case "k", "up":
			if t.cursor > 0 {
				t.cursor--
			}
		}
	}
	return t, nil
}

// Resize lets the renderer know the available main-region size on
// startup and after every WindowSizeMsg.
func (t todoApp) Resize(width, height int) tui.AppRenderer {
	t.width, t.height = width, height
	return t
}

// Header is rendered on every frame; it can vary with state.
func (t todoApp) Header(width int) string {
	return fmt.Sprintf("todo (%d/%d)", t.cursor+1, len(t.items))
}

// Footer can hold a per-screen help line. The shell falls back to it
// when help mode is off.
func (t todoApp) Footer(width int) string {
	return "j/k navigate  q quit  ? help"
}

// ExampleAppShell builds a tiny "todo" app on top of AppShell and
// drives it with table-driven Update calls — no real terminal needed.
//
// In production code, the entry point looks like:
//
//	root := cli.New(cli.Config{..., DisableValidate: true})
//	shell := tui.NewAppShellFromRoot(todoApp{items: items}, root)
//	if _, err := shell.Run(ctx); err != nil { ... }
func ExampleAppShell() {
	app := todoApp{items: []string{"deploy v2", "rollback v1", "scale x3"}}
	shell := tui.NewAppShell(app, testTheme(), tui.WithSize(40, 8))

	// Drive a couple of keypresses to advance the cursor.
	updated, _ := shell.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	updated, _ = updated.(tui.AppShell).Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	v := updated.(tui.AppShell).View()
	fmt.Println("alt-screen:", v.AltScreen)
	fmt.Println("contains main item:", strings.Contains(v.Content, "scale x3"))
	fmt.Println("contains header:", strings.Contains(v.Content, "todo"))
	fmt.Println("contains footer:", strings.Contains(v.Content, "navigate"))

	// Output:
	// alt-screen: true
	// contains main item: true
	// contains header: true
	// contains footer: true
}
