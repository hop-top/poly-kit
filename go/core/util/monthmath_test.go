package util

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAddMonthsClamped(t *testing.T) {
	tests := []struct {
		name     string
		in       time.Time
		months   int
		expected time.Time
	}{
		{"zero is identity",
			time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC), 0,
			time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)},
		{"clamp jan 31 to feb 28",
			time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC), 1,
			time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"clamp jan 31 to feb 29 in leap year",
			time.Date(2028, 1, 31, 12, 0, 0, 0, time.UTC), 1,
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC)},
		{"clamp jan 30 to feb 28",
			time.Date(2026, 1, 30, 12, 0, 0, 0, time.UTC), 1,
			time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"clamp 31 to 30-day month",
			time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC), 1,
			time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)},
		{"no clamp needed",
			time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC), 1,
			time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)},
		{"forward across year boundary",
			time.Date(2026, 11, 30, 12, 0, 0, 0, time.UTC), 3,
			time.Date(2027, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"backward one month with clamp",
			time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), -1,
			time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"backward across year boundary with clamp",
			time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC), -2,
			time.Date(2025, 11, 30, 12, 0, 0, 0, time.UTC)},
		{"backward exactly to january",
			time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC), -2,
			time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)},
		{"backward wrapping to december",
			time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC), -1,
			time.Date(2025, 12, 15, 12, 0, 0, 0, time.UTC)},
		{"backward twelve months from leap day",
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC), -12,
			time.Date(2027, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"backward many years",
			time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC), -37,
			time.Date(2022, 12, 31, 12, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addMonthsClamped(tt.in, tt.months)
			assert.True(t, tt.expected.Equal(got),
				"expected: %s\ngot:      %s", tt.expected, got)
		})
	}
}

func TestAddMonthsClamped_PreservesClock(t *testing.T) {
	loc := time.FixedZone("TEST", 3*3600)
	in := time.Date(2026, 1, 31, 23, 59, 58, 987654321, loc)

	got := addMonthsClamped(in, 1)

	assert.Equal(t, 2026, got.Year())
	assert.Equal(t, time.February, got.Month())
	assert.Equal(t, 28, got.Day())
	assert.Equal(t, 23, got.Hour())
	assert.Equal(t, 59, got.Minute())
	assert.Equal(t, 58, got.Second())
	assert.Equal(t, 987654321, got.Nanosecond())
	assert.Equal(t, loc.String(), got.Location().String())
}

func TestAddYearsClamped(t *testing.T) {
	tests := []struct {
		name     string
		in       time.Time
		years    int
		expected time.Time
	}{
		{"leap day forward to non-leap year",
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC), 1,
			time.Date(2029, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"leap day backward to non-leap year",
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC), -1,
			time.Date(2027, 2, 28, 12, 0, 0, 0, time.UTC)},
		{"leap day to leap day",
			time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC), 4,
			time.Date(2032, 2, 29, 12, 0, 0, 0, time.UTC)},
		{"ordinary date forward",
			time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC), 2,
			time.Date(2028, 4, 19, 12, 0, 0, 0, time.UTC)},
		{"1900 is not a leap year",
			time.Date(1904, 2, 29, 12, 0, 0, 0, time.UTC), -4,
			time.Date(1900, 2, 28, 12, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addYearsClamped(tt.in, tt.years)
			assert.True(t, tt.expected.Equal(got),
				"expected: %s\ngot:      %s", tt.expected, got)
		})
	}
}

func TestDaysInMonth(t *testing.T) {
	assert.Equal(t, 31, daysInMonth(2026, time.January))
	assert.Equal(t, 28, daysInMonth(2026, time.February))
	assert.Equal(t, 29, daysInMonth(2028, time.February))
	assert.Equal(t, 28, daysInMonth(1900, time.February)) // century, not leap
	assert.Equal(t, 29, daysInMonth(2000, time.February)) // 400-year rule
	assert.Equal(t, 30, daysInMonth(2026, time.April))
	assert.Equal(t, 31, daysInMonth(2026, time.December))
}

func TestFloorDivMod(t *testing.T) {
	cases := []struct{ a, b, div, mod int }{
		{0, 12, 0, 0},
		{11, 12, 0, 11},
		{12, 12, 1, 0},
		{13, 12, 1, 1},
		{-1, 12, -1, 11},
		{-12, 12, -1, 0},
		{-13, 12, -2, 11},
	}
	for _, c := range cases {
		assert.Equal(t, c.div, floorDiv(c.a, c.b),
			"floorDiv(%d, %d)", c.a, c.b)
		assert.Equal(t, c.mod, floorMod(c.a, c.b),
			"floorMod(%d, %d)", c.a, c.b)
	}
}
