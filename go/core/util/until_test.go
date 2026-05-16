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
		{"in 1 month", "in 1 month", now.AddDate(0, 1, 0)},
		{"in 6 months", "in 6 months", now.AddDate(0, 6, 0)},
		{"in 1 year", "in 1 year", now.AddDate(1, 0, 0)},
		{"in 2 years", "in 2 years", now.AddDate(2, 0, 0)},
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
		{"+3M", "+3M", now.AddDate(0, 3, 0)},
		{"+1y", "+1y", now.AddDate(1, 0, 0)},
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
