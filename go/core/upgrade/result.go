package upgrade

import "time"

// Result holds the outcome of a version check.
type Result struct {
	Current     string
	Latest      string
	URL         string
	ChecksumURL string
	Notes       string
	CheckedAt   time.Time
	UpdateAvail bool
	Err         error
}
