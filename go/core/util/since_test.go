package util

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSince(t *testing.T) {
	// fixed "now" for deterministic tests
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		input    string
		expected time.Time
	}{
		// git-style relative: "N <unit> ago"
		{"1 day ago", "1 day ago", now.AddDate(0, 0, -1)},
		{"3 days ago", "3 days ago", now.AddDate(0, 0, -3)},
		{"1 week ago", "1 week ago", now.AddDate(0, 0, -7)},
		{"2 weeks ago", "2 weeks ago", now.AddDate(0, 0, -14)},
		// month/year expectations are literals, not now.AddDate(...):
		// a computed expectation would silently track whatever the
		// implementation does and prove nothing about clamping.
		{"1 month ago", "1 month ago",
			time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)},
		{"6 months ago", "6 months ago",
			time.Date(2025, 10, 19, 12, 0, 0, 0, time.UTC)},
		{"1 year ago", "1 year ago",
			time.Date(2025, 4, 19, 12, 0, 0, 0, time.UTC)},
		{"2 years ago", "2 years ago",
			time.Date(2024, 4, 19, 12, 0, 0, 0, time.UTC)},
		{"1 hour ago", "1 hour ago", now.Add(-1 * time.Hour)},
		{"3 hours ago", "3 hours ago", now.Add(-3 * time.Hour)},
		{"30 minutes ago", "30 minutes ago", now.Add(-30 * time.Minute)},
		{"5 seconds ago", "5 seconds ago", now.Add(-5 * time.Second)},

		// named relative
		{"yesterday", "yesterday", now.AddDate(0, 0, -1)},

		// natural language relative dates (now = Sun 2026-04-19 12:00 UTC)
		{"last friday", "last friday", time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC)},
		{"last week", "last week", time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)},

		// short relative (USP-style)
		{"7d", "7d", now.AddDate(0, 0, -7)},
		{"24h", "24h", now.Add(-24 * time.Hour)},
		{"30m", "30m", now.Add(-30 * time.Minute)},
		{"2w", "2w", now.AddDate(0, 0, -14)},
		{"3M", "3M", time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC)},
		{"1y", "1y", time.Date(2025, 4, 19, 12, 0, 0, 0, time.UTC)},

		// ISO 8601
		{"date only", "2026-04-15", time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)},
		{"datetime UTC", "2026-04-15T10:30:00Z",
			time.Date(2026, 4, 15, 10, 30, 0, 0, time.UTC)},
		{"datetime offset", "2026-04-15T10:30:00+05:00",
			time.Date(2026, 4, 15, 10, 30, 0, 0, time.FixedZone("", 5*3600))},
		{"datetime negative offset", "2026-04-15T10:30:00-04:00",
			time.Date(2026, 4, 15, 10, 30, 0, 0, time.FixedZone("", -4*3600))},

		// whitespace tolerance
		{"leading/trailing spaces", "  3 days ago  ", now.AddDate(0, 0, -3)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSinceAt(tt.input, now)
			require.NoError(t, err, "input: %q", tt.input)
			assert.Equal(t, tt.expected.Unix(), got.Unix(),
				"input: %q\nexpected: %s\ngot:      %s",
				tt.input, tt.expected, got)
		})
	}
}

// TestParseSince_MonthClamping pins backward month/year arithmetic to clamping
// rather than Go's default AddDate normalisation. Every expectation is a
// literal date: computing it with AddDate would just re-derive the behavior
// under test.
func TestParseSince_MonthClamping(t *testing.T) {
	tests := []struct {
		name     string
		now      time.Time
		input    string
		expected time.Time
	}{
		// backward clamp: Mar 31 - 1 month lands in February.
		// AddDate would give 2026-03-03.
		{"mar 31 minus 1 month, phrase",
			time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC),
			"1 month ago",
			time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"mar 31 minus 1 month, short form",
			time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC),
			"1M",
			time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)},

		// no clamp needed
		{"mar 15 minus 1 month, no clamp",
			time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			"1 month ago",
			time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)},
		{"mar 15 minus 1 month, short form, no clamp",
			time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			"1M",
			time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)},

		// leap day backward into a non-leap year
		{"feb 29 minus 1 year",
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC),
			"1 year ago",
			time.Date(2027, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"feb 29 minus 1 year, short form",
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC),
			"1y",
			time.Date(2027, 2, 28, 12, 0, 0, 0, time.UTC)},
		// 12 months back is the same as 1 year back
		{"feb 29 minus 12 months",
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC),
			"12 months ago",
			time.Date(2027, 2, 28, 12, 0, 0, 0, time.UTC)},

		// clamping backwards across a year boundary
		{"jan 31 minus 2 months",
			time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC),
			"2 months ago",
			time.Date(2025, 11, 30, 12, 0, 0, 0, time.UTC)},
		{"may 31 minus 1 month into 30-day month",
			time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
			"1 month ago",
			time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)},

		// time-of-day (and sub-second precision) survives a clamp
		{"time of day preserved through clamp",
			time.Date(2026, 3, 31, 23, 47, 13, 123456789, time.UTC),
			"1 month ago",
			time.Date(2026, 2, 28, 23, 47, 13, 123456789, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSinceAt(tt.input, tt.now)
			require.NoError(t, err, "input: %q", tt.input)
			assert.True(t, tt.expected.Equal(got),
				"input: %q\nnow:      %s\nexpected: %s\ngot:      %s",
				tt.input, tt.now, tt.expected, got)
		})
	}
}

// TestParseSince_ClampPreservesLocation checks that a clamped result keeps the
// input's location rather than silently reverting to UTC.
func TestParseSince_ClampPreservesLocation(t *testing.T) {
	loc := time.FixedZone("TEST", -4*3600)
	now := time.Date(2026, 3, 31, 9, 30, 0, 0, loc)

	got, err := ParseSinceAt("1 month ago", now)
	require.NoError(t, err)
	assert.Equal(t, loc.String(), got.Location().String())
	assert.Equal(t, "2026-02-28T09:30:00-04:00",
		got.Format(time.RFC3339))
}

func TestParseSince_Errors(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"garbage", "not a date"},
		{"negative number", "-3 days ago"},
		{"zero", "0 days ago"},
		{"missing unit", "3 ago"},
		{"unknown unit", "3 fortnights ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSinceAt(tt.input, now)
			assert.Error(t, err, "input: %q should error", tt.input)
		})
	}
}

func TestParseSince_Convenience(t *testing.T) {
	// ParseSince uses time.Now — just verify it doesn't error on valid input
	got, err := ParseSince("1 day ago")
	require.NoError(t, err)
	assert.False(t, got.IsZero())
}
