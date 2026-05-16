// Package dialog provides a dialog interface and overlay stack for layering
// modal dialogs over base TUI content.
package dialog

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Dialog is the interface for modal dialog components rendered via an Overlay.
type Dialog interface {
	// Update processes a message and returns an updated Dialog and command.
	Update(msg tea.Msg) (Dialog, tea.Cmd)
	// View renders the dialog content at the given dimensions.
	View(width, height int) string
	// Done reports whether the dialog has completed and should be popped.
	Done() bool
}

// Overlay manages a stack of Dialog instances. Messages are routed to the
// topmost dialog; completed dialogs are automatically popped. All methods
// return copies (value-receiver pattern).
type Overlay struct {
	stack []Dialog
}

// NewOverlay returns an empty Overlay.
func NewOverlay() Overlay {
	return Overlay{}
}

// Push returns a copy with the given dialog pushed onto the stack.
func (o Overlay) Push(d Dialog) Overlay {
	o.stack = append(sliceCopy(o.stack), d)
	return o
}

// Pop returns a copy with the topmost dialog removed.
func (o Overlay) Pop() Overlay {
	if len(o.stack) == 0 {
		return o
	}
	o.stack = sliceCopy(o.stack[:len(o.stack)-1])
	return o
}

// Active returns the topmost dialog, or nil if the stack is empty.
func (o Overlay) Active() Dialog {
	if len(o.stack) == 0 {
		return nil
	}
	return o.stack[len(o.stack)-1]
}

// IsActive reports whether there is at least one dialog on the stack.
func (o Overlay) IsActive() bool {
	return len(o.stack) > 0
}

// Update routes the message to the active dialog. If the dialog reports
// Done() after the update, it is automatically popped from the stack.
func (o Overlay) Update(msg tea.Msg) (Overlay, tea.Cmd) {
	if len(o.stack) == 0 {
		return o, nil
	}

	top := o.stack[len(o.stack)-1]
	updated, cmd := top.Update(msg)

	// Copy stack before mutating.
	o.stack = sliceCopy(o.stack)
	o.stack[len(o.stack)-1] = updated

	if updated.Done() {
		o.stack = o.stack[:len(o.stack)-1]
	}

	return o, cmd
}

// View renders the base content, then overlays the active dialog centered
// within the given dimensions. If no dialog is active, base is returned as-is.
func (o Overlay) View(base string, width, height int) string {
	if len(o.stack) == 0 {
		return base
	}

	top := o.stack[len(o.stack)-1]
	dialogContent := top.View(width, height)

	return centerOverlay(base, dialogContent, width, height)
}

// centerOverlay places dialogContent centered over baseContent within the
// given width x height grid.
func centerOverlay(base, dialog string, width, height int) string {
	baseLines := splitPad(base, width, height)
	dialogLines := strings.Split(dialog, "\n")

	dh := len(dialogLines)
	dw := maxLineWidth(dialogLines)

	// Vertical and horizontal centering offsets.
	topOffset := (height - dh) / 2
	if topOffset < 0 {
		topOffset = 0
	}
	leftOffset := (width - dw) / 2
	if leftOffset < 0 {
		leftOffset = 0
	}

	result := make([]string, len(baseLines))
	copy(result, baseLines)

	for i, dl := range dialogLines {
		row := topOffset + i
		if row >= height {
			break
		}
		baseLine := result[row]
		result[row] = overlayLine(baseLine, dl, leftOffset, width)
	}

	return strings.Join(result, "\n")
}

// splitPad splits text into lines and pads/truncates to exactly height lines,
// each of exactly width characters.
func splitPad(text string, width, height int) []string {
	lines := strings.Split(text, "\n")
	result := make([]string, height)
	for i := range height {
		if i < len(lines) {
			result[i] = padRight(lines[i], width)
		} else {
			result[i] = strings.Repeat(" ", width)
		}
	}
	return result
}

// overlayLine places overlay text on top of a base line at the given offset.
func overlayLine(base, overlay string, offset, width int) string {
	baseRunes := []rune(padRight(base, width))
	overlayRunes := []rune(overlay)

	for i, r := range overlayRunes {
		pos := offset + i
		if pos >= width {
			break
		}
		baseRunes[pos] = r
	}

	return string(baseRunes[:width])
}

// padRight pads s with spaces to at least width runes, or truncates if longer.
func padRight(s string, width int) string {
	runes := []rune(s)
	if len(runes) >= width {
		return string(runes[:width])
	}
	return s + strings.Repeat(" ", width-len(runes))
}

// maxLineWidth returns the rune length of the longest line.
func maxLineWidth(lines []string) int {
	max := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > max {
			max = n
		}
	}
	return max
}

// sliceCopy returns a shallow copy of the dialog slice to preserve immutability.
func sliceCopy(s []Dialog) []Dialog {
	c := make([]Dialog, len(s))
	copy(c, s)
	return c
}
