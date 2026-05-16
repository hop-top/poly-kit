package ps

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"
)

// Status represents the current state of a tracked process entry.
type Status string

const (
	StatusRunning Status = "running"
	StatusPending Status = "pending"
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
	// StatusStopped indicates a process whose PID file is on disk but
	// the process itself is no longer alive (e.g. crashed without
	// removing its pidfile). Used by [EntryFromPIDFile].
	StatusStopped Status = "stopped"
)

// Progress tracks completion of a counted workload.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Entry is a single process status row reported by a Provider.
type Entry struct {
	ID       string    `json:"id"                table:"ID"`
	Status   Status    `json:"status"            table:"Status"`
	Worker   string    `json:"worker"            table:"Worker"`
	Worktree string    `json:"worktree,omitempty"`
	Track    string    `json:"track,omitempty"`
	Scope    string    `json:"scope"             table:"Scope"`
	Started  time.Time `json:"started"           table:"Duration"`
	Progress *Progress `json:"progress,omitempty" table:"Progress"`
}

// StatusColor returns the lipgloss style for a status.
func StatusColor(s Status) lipgloss.Style {
	switch s {
	case StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	case StatusPending:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	case StatusBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	case StatusDone:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	default:
		return lipgloss.NewStyle()
	}
}

// Duration returns a human-readable elapsed time since Started.
func (e Entry) Duration() string {
	d := time.Since(e.Started)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// DurationFrom returns human-readable elapsed time from a reference point.
// Useful for deterministic testing.
func (e Entry) DurationFrom(now time.Time) string {
	d := now.Sub(e.Started)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// ProgressString returns "3/10 (30%)" or empty if no progress set.
func (e Entry) ProgressString() string {
	if e.Progress == nil {
		return ""
	}
	pct := 0
	if e.Progress.Total > 0 {
		pct = e.Progress.Done * 100 / e.Progress.Total
	}
	return fmt.Sprintf("%d/%d (%d%%)", e.Progress.Done, e.Progress.Total, pct)
}

// TruncateScope returns scope truncated to maxLen with ellipsis.
// If maxLen <= 0, returns "".
func TruncateScope(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
