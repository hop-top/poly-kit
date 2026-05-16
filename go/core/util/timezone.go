package util

import (
	"fmt"
	"time"
)

// LoadTimezone resolves a timezone name into a *time.Location. The two
// conventional names are recognized without consulting the tzdata
// database:
//
//   - "" or "local" → time.Local
//   - "UTC" → time.UTC
//
// Any other value is passed through to time.LoadLocation, which returns
// an error for unknown IANA names. Callers should treat the returned
// error as a user-facing config error.
func LoadTimezone(name string) (*time.Location, error) {
	switch name {
	case "", "local":
		return time.Local, nil
	case "UTC":
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("timeutil: invalid timezone %q: %w", name, err)
	}
	return loc, nil
}

// FormatInZone renders t in the given location using layout. When loc
// is nil the system local timezone is used. The layout follows the
// stdlib time.Format reference syntax.
//
// This is a thin wrapper over time.Time.In + Format that exists so
// every display path in a kit-based CLI goes through the same
// formatting primitive, making timezone behavior auditable and
// consistent across tools.
func FormatInZone(t time.Time, loc *time.Location, layout string) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format(layout)
}
