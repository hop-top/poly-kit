package upgrade

import "time"

// Result holds the outcome of a version check.
type Result struct {
	Current     string
	Latest      string
	URL         string
	ChecksumURL string
	Notes       string
	// Tag is the git tag of the Latest release; set only when releases
	// are listed (WithTagPrefix, WithIncludePrereleases).
	Tag         string
	CheckedAt   time.Time
	UpdateAvail bool
	Err         error
}
