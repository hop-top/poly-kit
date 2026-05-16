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
		{"1 month ago", "1 month ago", now.AddDate(0, -1, 0)},
		{"6 months ago", "6 months ago", now.AddDate(0, -6, 0)},
		{"1 year ago", "1 year ago", now.AddDate(-1, 0, 0)},
		{"2 years ago", "2 years ago", now.AddDate(-2, 0, 0)},
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
		{"3M", "3M", now.AddDate(0, -3, 0)},
		{"1y", "1y", now.AddDate(-1, 0, 0)},

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
