package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Item is a type alias for Renderer, used by List to render its entries.
type Item = Renderer

// List is a generic scrollable list of items. It is a sub-component:
// View returns a plain string, not tea.View. All mutating methods return
// copies (value-receiver pattern), matching Progress and other tui components.
type List struct {
	items  []Item
	offset int
	height int
	follow bool
}

// NewList returns a List with the given visible height (number of lines).
func NewList(height int) List {
	if height < 1 {
		height = 1
	}
	return List{height: height}
}

// SetItems returns a copy with the given items. When follow mode is active
// and the list was already scrolled to the bottom, the offset auto-advances
// so the newest items are visible.
func (l List) SetItems(items []Item) List {
	atBottom := l.atBottom()
	l.items = items
	if l.follow && atBottom {
		l = l.ScrollToEnd()
	}
	l = l.clampOffset()
	return l
}

// Items returns the current items.
func (l List) Items() []Item { return l.items }

// Offset returns the current scroll offset.
func (l List) Offset() int { return l.offset }

// Height returns the visible height.
func (l List) Height() int { return l.height }

// Follow returns whether follow (auto-scroll) mode is enabled.
func (l List) Follow() bool { return l.follow }

// SetFollow returns a copy with follow mode set to the given value.
func (l List) SetFollow(v bool) List {
	l.follow = v
	return l
}

// SetHeight returns a copy with the given visible height.
func (l List) SetHeight(h int) List {
	if h < 1 {
		h = 1
	}
	l.height = h
	l = l.clampOffset()
	return l
}

// ScrollBy returns a copy scrolled by n lines (positive = down, negative = up).
func (l List) ScrollBy(n int) List {
	l.offset += n
	l = l.clampOffset()
	return l
}

// ScrollToEnd returns a copy scrolled to the bottom.
func (l List) ScrollToEnd() List {
	l.offset = l.maxOffset()
	return l
}

// Update handles mouse wheel messages for scrolling.
func (l List) Update(msg tea.Msg) (List, tea.Cmd) {
	if wm, ok := msg.(tea.MouseWheelMsg); ok {
		m := tea.Mouse(wm)
		switch m.Button {
		case tea.MouseWheelUp:
			l = l.ScrollBy(-1)
		case tea.MouseWheelDown:
			l = l.ScrollBy(1)
		}
	}
	return l, nil
}

// View renders the visible slice of items within the list height.
func (l List) View(width int) string {
	if len(l.items) == 0 {
		return ""
	}

	end := l.offset + l.height
	if end > len(l.items) {
		end = len(l.items)
	}

	visible := l.items[l.offset:end]
	var b strings.Builder
	for i, item := range visible {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(item.Render(width))
	}
	return b.String()
}

// maxOffset returns the maximum valid scroll offset.
func (l List) maxOffset() int {
	max := len(l.items) - l.height
	if max < 0 {
		return 0
	}
	return max
}

// clampOffset ensures the offset stays within valid bounds.
func (l List) clampOffset() List {
	if l.offset < 0 {
		l.offset = 0
	}
	if max := l.maxOffset(); l.offset > max {
		l.offset = max
	}
	return l
}

// atBottom reports whether the list is scrolled to the bottom.
func (l List) atBottom() bool {
	return l.offset >= l.maxOffset()
}
