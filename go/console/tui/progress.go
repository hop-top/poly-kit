package tui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
)

const (
	progressDefaultWidth = 40
	progressFullChar     = "█"
	progressEmptyChar    = "░"
)

// Progress is a themed progress bar model.
// bubbles/v2 does not ship a progress component yet, so this is a
// self-contained implementation backed by bubbletea/v2.
type Progress struct {
	percent float64
	width   int

	full  lipgloss.Style
	empty lipgloss.Style
}

// NewProgress returns a Progress bar styled with a gradient from
// theme.Accent (filled) to theme.Muted (empty track).
func NewProgress(theme cli.Theme) Progress {
	return Progress{
		width: progressDefaultWidth,
		full:  lipgloss.NewStyle().Foreground(theme.Accent),
		empty: lipgloss.NewStyle().Foreground(theme.Muted),
	}
}

// ProgressMsg sets the progress bar percentage (0.0 – 1.0).
type ProgressMsg float64

// SetPercent returns a copy with the given percentage clamped to [0,1].
func (p Progress) SetPercent(v float64) Progress {
	p.percent = clamp01(v)
	return p
}

// SetWidth returns a copy with the given character width.
func (p Progress) SetWidth(w int) Progress {
	if w < 1 {
		w = 1
	}
	p.width = w
	return p
}

// Percent returns the current percentage.
func (p Progress) Percent() float64 { return p.percent }

// Update handles ProgressMsg to update the percentage.
func (p Progress) Update(msg tea.Msg) (Progress, tea.Cmd) {
	if pm, ok := msg.(ProgressMsg); ok {
		p.percent = clamp01(float64(pm))
	}
	return p, nil
}

// View renders the progress bar.
func (p Progress) View() string {
	filled := int(p.percent * float64(p.width))
	empty := p.width - filled
	return p.full.Render(strings.Repeat(progressFullChar, filled)) +
		p.empty.Render(strings.Repeat(progressEmptyChar, empty))
}

// ViewWithColor renders the progress bar using the provided fill and
// track colors, overriding the theme defaults for a single render.
func (p Progress) ViewWithColor(fill, track color.Color) string {
	filled := int(p.percent * float64(p.width))
	empty := p.width - filled
	fs := lipgloss.NewStyle().Foreground(fill)
	es := lipgloss.NewStyle().Foreground(track)
	return fs.Render(strings.Repeat(progressFullChar, filled)) +
		es.Render(strings.Repeat(progressEmptyChar, empty))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
