// Package tui is the TUI component library for kit CLIs.
//
// It provides pre-themed bubbletea/v2 components (Badge, Confirm, Progress,
// List, Dialog, Overlay) and a set of capability interfaces that components
// may implement: Renderer, Focusable, and Animatable.
//
// Components follow the value-receiver/copy-on-write pattern from bubbletea
// and use cli.Theme for consistent styling across all kit applications.
package tui

import tea "charm.land/bubbletea/v2"

// Renderer is implemented by components that can render themselves to a
// string at a given terminal width.
type Renderer interface {
	Render(width int) string
}

// Focusable is an opt-in capability for components that support keyboard
// focus. Focus returns a tea.Cmd for any initialization needed when the
// component gains focus.
type Focusable interface {
	Focus() tea.Cmd
	Blur()
	Focused() bool
}

// Animatable is an opt-in capability for components that drive frame-based
// animation (spinners, progress pulses, etc.). Tick returns the tea.Cmd
// that schedules the next animation frame.
type Animatable interface {
	Tick() tea.Cmd
}
