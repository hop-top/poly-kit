package util

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUntil(t *testing.T) {
	// fixed "now" for deterministic tests — Saturday
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		input    string
		expected time.Time
	}{
		// named relative
		{"tomorrow", "tomorrow", now.AddDate(0, 0, 1)},

		// "in N <unit>"
		{"in 1 day", "in 1 day", now.AddDate(0, 0, 1)},
		{"in 3 days", "in 3 days", now.AddDate(0, 0, 3)},
		{"in 1 week", "in 1 week", now.AddDate(0, 0, 7)},
		{"in 2 weeks", "in 2 weeks", now.AddDate(0, 0, 14)},
		// month/year expectations are literals, not now.AddDate(...):
		// a computed expectation would silently track whatever the
		// implementation does and prove nothing about clamping.
		{"in 1 month", "in 1 month",
			time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)},
		{"in 6 months", "in 6 months",
			time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)},
		{"in 1 year", "in 1 year",
			time.Date(2027, 4, 19, 12, 0, 0, 0, time.UTC)},
		{"in 2 years", "in 2 years",
			time.Date(2028, 4, 19, 12, 0, 0, 0, time.UTC)},
		{"in 1 hour", "in 1 hour", now.Add(1 * time.Hour)},
		{"in 3 hours", "in 3 hours", now.Add(3 * time.Hour)},
		{"in 30 minutes", "in 30 minutes",
			now.Add(30 * time.Minute)},
		{"in 5 seconds", "in 5 seconds",
			now.Add(5 * time.Second)},

		// short relative: +Nd, +Nh, etc.
		{"+3d", "+3d", now.AddDate(0, 0, 3)},
		{"+24h", "+24h", now.Add(24 * time.Hour)},
		{"+30m", "+30m", now.Add(30 * time.Minute)},
		{"+2w", "+2w", now.AddDate(0, 0, 14)},
		{"+3M", "+3M", time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)},
		{"+1y", "+1y", time.Date(2027, 4, 19, 12, 0, 0, 0, time.UTC)},
		{"+10s", "+10s", now.Add(10 * time.Second)},

		// weekday names (now = Sunday 2026-04-19)
		{"monday", "monday", now.AddDate(0, 0, 1)},
		{"tuesday", "tuesday", now.AddDate(0, 0, 2)},
		{"wednesday", "wednesday", now.AddDate(0, 0, 3)},
		{"thursday", "thursday", now.AddDate(0, 0, 4)},
		{"friday", "friday", now.AddDate(0, 0, 5)},
		{"saturday", "saturday", now.AddDate(0, 0, 6)},
		{"sunday", "sunday", now.AddDate(0, 0, 7)},
		{"Friday uppercase", "Friday", now.AddDate(0, 0, 5)},

		// natural language relative dates (now = Sun 2026-04-19 12:00 UTC)
		{"next monday", "next monday", time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)},
		{"next friday", "next friday", time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)},
		{"next week", "next week", time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)},

		// ISO 8601
		{"date only", "2026-05-01",
			time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
		{"datetime UTC", "2026-05-01T10:30:00Z",
			time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC)},
		{"datetime offset", "2026-05-01T10:30:00+05:00",
			time.Date(2026, 5, 1, 10, 30, 0, 0,
				time.FixedZone("", 5*3600))},

		// common absolute formats (space-separated)
		{"date time space sep", "2026-05-01 10:30:00",
			time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC)},
		{"date time space no seconds", "2026-05-01 10:30",
			time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC)},

		// T separator without seconds
		{"datetime T no seconds", "2026-05-01T10:30",
			time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC)},
		{"datetime T with offset no seconds", "2026-05-01T10:30+05:00",
			time.Date(2026, 5, 1, 10, 30, 0, 0,
				time.FixedZone("", 5*3600))},

		// whitespace tolerance
		{"leading/trailing spaces", "  in 3 days  ",
			now.AddDate(0, 0, 3)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUntilAt(tt.input, now)
			require.NoError(t, err, "input: %q", tt.input)
			assert.Equal(t, tt.expected.Unix(), got.Unix(),
				"input: %q\nexpected: %s\ngot:      %s",
				tt.input, tt.expected, got)
		})
	}
}

// TestParseUntil_MonthClamping pins forward month/year arithmetic to clamping
// rather than Go's default AddDate normalisation. Every expectation is a
// literal date: computing it with AddDate would just re-derive the behaviour
// under test.
func TestParseUntil_MonthClamping(t *testing.T) {
	tests := []struct {
		name     string
		now      time.Time
		input    string
		expected time.Time
	}{
		// headline: Jan 31 + 1 month lands in February, not March.
		// AddDate would give 2026-03-03.
		{"jan 31 plus 1 month, phrase",
			time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC),
			"in 1 month",
			time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)},
		// same case through the short-form path — different code path
		{"jan 31 plus 1 month, short form",
			time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC),
			"+1M",
			time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)},

		// no clamp needed: ordinary month arithmetic is unaffected
		{"jan 15 plus 1 month, no clamp",
			time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
			"in 1 month",
			time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)},
		{"jan 15 plus 1 month, short form, no clamp",
			time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
			"+1M",
			time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)},

		// leap day forward into a non-leap year
		{"feb 29 plus 1 year",
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC),
			"in 1 year",
			time.Date(2029, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"feb 29 plus 1 year, short form",
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC),
			"+1y",
			time.Date(2029, 2, 28, 12, 0, 0, 0, time.UTC)},

		// clamping across a year boundary and into a leap February
		{"dec 31 plus 2 months",
			time.Date(2027, 12, 31, 12, 0, 0, 0, time.UTC),
			"in 2 months",
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC)},
		{"aug 31 plus 1 month into 30-day month",
			time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
			"in 1 month",
			time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)},

		// time-of-day (and sub-second precision) survives a clamp
		{"time of day preserved through clamp",
			time.Date(2026, 1, 31, 23, 47, 13, 123456789, time.UTC),
			"in 1 month",
			time.Date(2026, 2, 28, 23, 47, 13, 123456789, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUntilAt(tt.input, tt.now)
			require.NoError(t, err, "input: %q", tt.input)
			assert.True(t, tt.expected.Equal(got),
				"input: %q\nnow:      %s\nexpected: %s\ngot:      %s",
				tt.input, tt.now, tt.expected, got)
		})
	}
}

// TestParseUntil_ClampPreservesLocation checks that a clamped result keeps the
// input's location rather than silently reverting to UTC.
func TestParseUntil_ClampPreservesLocation(t *testing.T) {
	loc := time.FixedZone("TEST", 5*3600)
	now := time.Date(2026, 1, 31, 9, 30, 0, 0, loc)

	got, err := ParseUntilAt("in 1 month", now)
	require.NoError(t, err)
	assert.Equal(t, loc.String(), got.Location().String())
	assert.Equal(t, "2026-02-28T09:30:00+05:00",
		got.Format(time.RFC3339))
}

func TestParseUntil_Errors(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"garbage", "not a date"},
		{"negative count", "in -3 days"},
		{"zero count", "in 0 days"},
		{"missing unit", "in 3"},
		{"unknown unit", "in 3 fortnights"},
		{"invalid short", "+0d"},
		{"negative short", "+-3d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseUntilAt(tt.input, now)
			assert.Error(t, err,
				"input: %q should error", tt.input)
		})
	}
}

func TestParseUntil_Convenience(t *testing.T) {
	got, err := ParseUntil("in 1 day")
	require.NoError(t, err)
	assert.False(t, got.IsZero())
}

func TestParseUntil_Integration(t *testing.T) {
	now := time.Now()

	// "tomorrow" should be ~24h from now
	got, err := ParseUntil("tomorrow")
	require.NoError(t, err)
	diff := got.Sub(now)
	assert.InDelta(t, 24*time.Hour, diff, float64(time.Minute),
		"tomorrow should be ~24h from now")

	// "+1h" should be ~1h from now
	got, err = ParseUntil("+1h")
	require.NoError(t, err)
	diff = got.Sub(now)
	assert.InDelta(t, time.Hour, diff, float64(time.Minute),
		"+1h should be ~1h from now")

	// ISO roundtrip: future date parses to itself
	future := now.Add(48 * time.Hour).Format("2006-01-02")
	got, err = ParseUntil(future)
	require.NoError(t, err)
	assert.Equal(t, future, got.Format("2006-01-02"))

	// ParseSince and ParseUntil are symmetric:
	// ParseSince("1 day ago") ≈ now - 24h
	// ParseUntil("in 1 day") ≈ now + 24h
	since, err := ParseSince("1 day ago")
	require.NoError(t, err)
	until, err := ParseUntil("in 1 day")
	require.NoError(t, err)
	span := until.Sub(since)
	assert.InDelta(t, 48*time.Hour, span, float64(time.Minute),
		"since(-1d) to until(+1d) should span ~48h")
}
