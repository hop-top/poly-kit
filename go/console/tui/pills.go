package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/tui/styles"
)

// Pill represents a single compact inline indicator with a label and value.
type Pill struct {
	label string
	value string
}

// NewPill returns a Pill with the given label and value.
func NewPill(label, value string) Pill {
	return Pill{label: label, value: value}
}

// Label returns the pill's label.
func (p Pill) Label() string { return p.label }

// Value returns the pill's current value.
func (p Pill) Value() string { return p.value }

// SetValue returns a copy with the value updated.
func (p Pill) SetValue(v string) Pill {
	p.value = v
	return p
}

// Render returns the pill as a styled string. Focused pills get a
// rounded border; unfocused pills get a hidden border.
func (p Pill) Render(theme cli.Theme, focused bool) string {
	s := styles.NewStyles(theme)
	text := lipgloss.NewStyle().Bold(true).Render(p.label) + " " + p.value
	if focused {
		return s.Pills.Focused.Render(text)
	}
	return s.Pills.Blurred.Render(text)
}

// PillBar holds a slice of pills and tracks focus and expanded state.
type PillBar struct {
	pills           []Pill
	focused         int
	expanded        bool
	expandedContent string
	toggleKey       string
}

// NewPillBar returns a PillBar containing the given pills.
func NewPillBar(pills ...Pill) PillBar {
	return PillBar{
		pills:     pills,
		toggleKey: "ctrl+t",
	}
}

// Toggle returns a copy with the expanded state toggled.
func (b PillBar) Toggle() PillBar {
	b.expanded = !b.expanded
	return b
}

// Expanded reports whether the pill bar is in expanded state.
func (b PillBar) Expanded() bool { return b.expanded }

// FocusNext returns a copy with focus moved to the next pill,
// wrapping around to the first.
func (b PillBar) FocusNext() PillBar {
	if len(b.pills) == 0 {
		return b
	}
	b.focused = (b.focused + 1) % len(b.pills)
	return b
}

// FocusPrev returns a copy with focus moved to the previous pill,
// wrapping around to the last.
func (b PillBar) FocusPrev() PillBar {
	if len(b.pills) == 0 {
		return b
	}
	b.focused = (b.focused - 1 + len(b.pills)) % len(b.pills)
	return b
}

// FocusedIndex returns the index of the currently focused pill.
func (b PillBar) FocusedIndex() int { return b.focused }

// SetExpanded returns a copy with the expanded detail content set.
// This content is rendered below the pills row when expanded.
func (b PillBar) SetExpanded(content string) PillBar {
	b.expandedContent = content
	return b
}

// Pills returns the current slice of pills.
func (b PillBar) Pills() []Pill { return b.pills }

// Update handles key messages: configurable toggle key (default ctrl+t),
// and left/right arrows to switch focus when expanded.
func (b PillBar) Update(msg tea.Msg) (PillBar, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return b, nil
	}

	switch key.String() {
	case b.toggleKey:
		b = b.Toggle()
	case "left":
		if b.expanded {
			b = b.FocusPrev()
		}
	case "right":
		if b.expanded {
			b = b.FocusNext()
		}
	}

	return b, nil
}

// View renders the pill bar. Compact mode shows a horizontal row of
// pills plus a help hint. Expanded mode adds the expanded content
// below the pills row.
func (b PillBar) View(width int) string {
	if len(b.pills) == 0 {
		return ""
	}

	theme := cli.Theme{} // zero theme used only for structure
	_ = theme

	return b.viewWithTheme(cli.Theme{}, width)
}

// ViewWithTheme renders the pill bar using the given theme.
func (b PillBar) ViewWithTheme(theme cli.Theme, width int) string {
	return b.viewWithTheme(theme, width)
}

func (b PillBar) viewWithTheme(theme cli.Theme, width int) string {
	if len(b.pills) == 0 {
		return ""
	}

	s := styles.NewStyles(theme)

	// Build pills row.
	var parts []string
	for i, pill := range b.pills {
		parts = append(parts, pill.Render(theme, i == b.focused))
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, parts...)

	// Help hint.
	action := "open"
	if b.expanded {
		action = "close"
	}
	hint := s.Pills.HelpKey.Render(b.toggleKey) + " " +
		s.Pills.HelpText.Render(action)

	pillsLine := row + " " + hint

	if !b.expanded {
		return s.Pills.Area.Render(pillsLine)
	}

	// Expanded: pills row + content below.
	var buf strings.Builder
	buf.WriteString(pillsLine)
	if b.expandedContent != "" {
		buf.WriteByte('\n')
		buf.WriteString(b.expandedContent)
	}
	return s.Pills.Area.Render(buf.String())
}

// Height returns the current height of the pill bar: 1 when compact,
// 1 + number of expanded content lines when expanded.
func (b PillBar) Height() int {
	if !b.expanded || b.expandedContent == "" {
		return 1
	}
	return 1 + strings.Count(b.expandedContent, "\n") + 1
}
