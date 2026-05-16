package ps_test

import (
	"fmt"
	"os"
	"time"

	"hop.top/kit/go/console/ps"
)

func Example() {
	entries := []ps.Entry{
		{
			ID:       "job-42",
			Status:   ps.StatusRunning,
			Worker:   "agent-alpha",
			Scope:    "deploy/staging",
			Started:  time.Now().Add(-3 * time.Minute),
			Progress: &ps.Progress{Done: 7, Total: 12},
		},
		{
			ID:      "job-41",
			Status:  ps.StatusPending,
			Worker:  "agent-beta",
			Scope:   "lint/check",
			Started: time.Now().Add(-30 * time.Second),
		},
	}

	// Quiet mode: just IDs
	_ = ps.Render(os.Stdout, entries, "quiet", false)
	fmt.Println("---")

	// Progress string
	fmt.Println(entries[0].ProgressString())

	// Output:
	// job-42
	// job-41
	// ---
	// 7/12 (58%)
}
