package tui

import (
	"time"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/tui/styles"
)

// DefaultStatusTTL is the default time-to-live for ephemeral info messages.
const DefaultStatusTTL = 5 * time.Second

// InfoType classifies an ephemeral status message.
type InfoType int

const (
	InfoTypeInfo    InfoType = iota // ℹ  accent
	InfoTypeError                   // ●  error/red
	InfoTypeWarn                    // ▲  warn/secondary
	InfoTypeSuccess                 // ✓  success/green
)

// InfoMsg carries an ephemeral message to overlay on the status bar.
type InfoMsg struct {
	Type InfoType
	Msg  string
}

// ClearStatusMsg is sent after a TTL expires to clear the info message.
type ClearStatusMsg struct{}

// ClearInfoAfter returns a tea.Cmd that fires ClearStatusMsg after ttl.
func ClearInfoAfter(ttl time.Duration) tea.Cmd {
	return tea.Tick(ttl, func(time.Time) tea.Msg {
		return ClearStatusMsg{}
	})
}

// Status renders a help-keybinding line with optional ephemeral messages.
// It wraps bubbles/v2/help.Model and follows the value-receiver,
// copy-on-write pattern used by all tui components.
type Status struct {
	help   help.Model
	keymap help.KeyMap
	styles styles.StatusStyles
	info   *InfoMsg
}

// NewStatus creates a Status wired to the given theme and key map.
func NewStatus(theme cli.Theme, km help.KeyMap) Status {
	ss := styles.NewStyles(theme)
	h := help.New()
	h.Styles.ShortKey = ss.Accent
	h.Styles.ShortDesc = ss.Muted
	h.Styles.ShortSeparator = ss.Muted
	h.Styles.FullKey = ss.Accent
	h.Styles.FullDesc = ss.Muted
	h.Styles.FullSeparator = ss.Muted

	return Status{
		help:   h,
		keymap: km,
		styles: ss.Status,
	}
}

// View renders the status bar at the given width. When an InfoMsg is
// set it takes priority over the help line.
func (s Status) View(width int) string {
	if s.info != nil {
		return s.renderInfo(width)
	}
	s.help.SetWidth(width)
	return s.help.View(s.keymap)
}

// renderInfo formats the current info message with its indicator.
func (s Status) renderInfo(_ int) string {
	if s.info == nil {
		return ""
	}
	var indicator, msg string
	switch s.info.Type {
	case InfoTypeError:
		indicator = s.styles.ErrorIndicator.Render()
		msg = s.styles.ErrorMessage.Render(s.info.Msg)
	case InfoTypeWarn:
		indicator = s.styles.WarnIndicator.Render()
		msg = s.styles.WarnMessage.Render(s.info.Msg)
	case InfoTypeSuccess:
		indicator = s.styles.SuccessIndicator.Render()
		msg = s.styles.SuccessMessage.Render(s.info.Msg)
	default: // InfoTypeInfo
		indicator = s.styles.InfoIndicator.Render()
		msg = s.styles.InfoMessage.Render(s.info.Msg)
	}
	return indicator + msg
}

// ToggleHelp switches between compact and expanded help views.
func (s Status) ToggleHelp() Status {
	s.help.ShowAll = !s.help.ShowAll
	return s
}

// ShowingAll reports whether the expanded help view is active.
func (s Status) ShowingAll() bool {
	return s.help.ShowAll
}

// SetWidth updates the help model width.
func (s Status) SetWidth(w int) Status {
	s.help.SetWidth(w)
	return s
}

// SetInfoMsg sets an ephemeral message that renders over the help line.
func (s Status) SetInfoMsg(msg InfoMsg) Status {
	s.info = &msg
	return s
}

// ClearInfoMsg removes the ephemeral message, restoring the help line.
func (s Status) ClearInfoMsg() Status {
	s.info = nil
	return s
}

// Update handles ClearStatusMsg to auto-clear ephemeral messages.
func (s Status) Update(msg tea.Msg) (Status, tea.Cmd) {
	switch msg.(type) {
	case ClearStatusMsg:
		s.info = nil
	}
	return s, nil
}
