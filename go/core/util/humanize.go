package util

import (
	"fmt"
	"time"
)

// HumanDuration formats a duration in human-readable form.
func HumanDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/(7*24)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(30*24)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(365*24)))
	}
}

// HumanBytes formats byte count in human-readable form.
func HumanBytes(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	switch {
	case b < kb:
		return fmt.Sprintf("%d B", b)
	case b < mb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	case b < gb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b < tb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	default:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(tb))
	}
}

// RelativeTime formats a time as relative to now.
func RelativeTime(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		return "in " + HumanDuration(-d)
	}
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	if d < 48*time.Hour {
		return "yesterday"
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
