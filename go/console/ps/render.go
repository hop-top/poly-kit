package ps

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// Render writes entries to w in the specified format.
//
// Supported formats: "table" (default), "json", "quiet".
// When noColor is true, table output omits ANSI styling.
func Render(w io.Writer, entries []Entry, format string, noColor bool) error {
	switch format {
	case "json":
		return renderJSON(w, entries)
	case "quiet":
		return renderQuiet(w, entries)
	default:
		return renderTable(w, entries, noColor, time.Now())
	}
}

// RenderAt is like Render but accepts a fixed "now" for deterministic output.
func RenderAt(w io.Writer, entries []Entry, format string, noColor bool, now time.Time) error {
	switch format {
	case "json":
		return renderJSON(w, entries)
	case "quiet":
		return renderQuiet(w, entries)
	default:
		return renderTable(w, entries, noColor, now)
	}
}

func renderJSON(w io.Writer, entries []Entry) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

func renderQuiet(w io.Writer, entries []Entry) error {
	for _, e := range entries {
		fmt.Fprintln(w, e.ID)
	}
	return nil
}

func renderTable(w io.Writer, entries []Entry, noColor bool, now time.Time) error {
	if len(entries) == 0 {
		return nil
	}

	hasWorktree := false
	hasTrack := false
	for _, e := range entries {
		if e.Worktree != "" {
			hasWorktree = true
		}
		if e.Track != "" {
			hasTrack = true
		}
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()

	// Header
	cols := []string{"ID", "STATUS", "WORKER", "SCOPE", "DURATION", "PROGRESS"}
	if hasWorktree {
		cols = append(cols, "WORKTREE")
	}
	if hasTrack {
		cols = append(cols, "TRACK")
	}
	fmt.Fprintln(tw, strings.Join(cols, "\t"))

	// Rows — render plain text into tabwriter, then colorize would break
	// alignment. Instead, use fixed-width status column to avoid ANSI byte
	// counting issues with tabwriter.
	for _, e := range entries {
		status := padStatus(e.Status, noColor)

		scope := TruncateScope(e.Scope, 40)
		dur := e.DurationFrom(now)
		prog := e.ProgressString()

		row := []string{e.ID, status, e.Worker, scope, dur, prog}
		if hasWorktree {
			row = append(row, e.Worktree)
		}
		if hasTrack {
			row = append(row, e.Track)
		}
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}

	return nil
}

// statusWidth is the max visible width of any status string.
const statusWidth = 7 // "running" / "pending" / "blocked"

// padStatus returns the status string padded to a fixed visible width.
// When noColor is false, ANSI color codes wrap the text but the visible
// content is already padded, so tabwriter alignment stays correct.
func padStatus(s Status, noColor bool) string {
	plain := string(s)
	padded := plain + strings.Repeat(" ", statusWidth-len(plain))
	if noColor {
		return padded
	}
	return StatusColor(s).Render(padded)
}
