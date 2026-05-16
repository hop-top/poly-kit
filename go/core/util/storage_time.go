package util

import (
	"fmt"
	"time"
)

// FormatStorageTime returns t encoded for at-rest storage: UTC RFC3339.
//
// Every kit-based tool that writes a time.Time to a TEXT column or a
// wire format expecting RFC3339 SHOULD funnel through this function.
// The contract is intentionally narrow: always UTC, always RFC3339,
// never a fixed offset. This preserves lexicographic ordering on TEXT
// indexes (a property the stdlib RFC3339 layout only guarantees when
// every value carries the same offset) and keeps round-trips
// timezone-stable across machines.
//
// Use ParseStorageTime to decode the result.
func FormatStorageTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// ParseStorageTime decodes an RFC3339 string produced by
// FormatStorageTime (or by any writer following the same contract)
// and returns a time.Time normalised to UTC.
//
// Accepts both RFC3339 and RFC3339Nano. Any value with a non-UTC
// offset is converted to UTC; callers receive a UTC time.Time
// regardless of how the stored string was encoded. This means the
// reader survives a writer that forgot .UTC() — the bug is corrected
// on the way in rather than propagating into in-memory comparisons.
//
// Returns a wrapped error on parse failure; callers should treat
// failures as data corruption (the row is bad).
func ParseStorageTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("timeutil: parse storage time %q: %w", s, err)
	}
	return t.UTC(), nil
}

// SameDayInZone reports whether a and b fall on the same calendar
// day when interpreted in loc. A nil loc resolves to time.Local.
//
// This is the predicate every "today" / "due today" / "fired today"
// bucket needs: comparing Date() across two time.Time values that
// live in different locations (typically UTC for storage vs. local
// or configured ui.timezone for display) silently misclassifies
// around midnight in the user's display zone. SameDayInZone forces
// both instants into loc before extracting Y/M/D so the comparison
// is wall-clock-consistent with what the user sees.
func SameDayInZone(a, b time.Time, loc *time.Location) bool {
	if loc == nil {
		loc = time.Local
	}
	ay, am, ad := a.In(loc).Date()
	by, bm, bd := b.In(loc).Date()
	return ay == by && am == bm && ad == bd
}
