package util

import "time"

// addMonthsClamped returns t shifted by months calendar months, clamping the
// day-of-month to the last valid day of the target month instead of rolling
// forward into the next one. months may be negative to move backwards.
//
// This exists because Go's time.AddDate normalises overflow rather than
// clamping: 2026-01-31 plus one month yields 2026-03-03, because "February
// 31st" is carried into March. That is surprising for a date utility — a user
// who writes "in 1 month" on January 31st means "some time in February", not
// "early March", and silently skipping the named month is the wrong answer.
//
// Clamping is also what the Rust runtime does (chrono's Months arithmetic
// clamps by default), so clamping here keeps the two runtimes in agreement by
// construction rather than leaving one of them with a documented exception.
//
// Time-of-day, sub-second precision, and location are preserved exactly.
// Years are expressed as months: N years = N*12 months.
//
// Note that time.Date normalises too, so the clamped day has to be computed
// explicitly before constructing the result.
func addMonthsClamped(t time.Time, months int) time.Time {
	year, month, day := t.Date()

	// Absolute month index, so negative counts borrow across year
	// boundaries without special-casing.
	total := int(month) - 1 + months
	targetYear := year + floorDiv(total, 12)
	targetMonth := time.Month(floorMod(total, 12) + 1)

	if max := daysInMonth(targetYear, targetMonth); day > max {
		day = max
	}

	return time.Date(targetYear, targetMonth, day,
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// addYearsClamped returns t shifted by years calendar years, clamping
// February 29th to February 28th in non-leap target years. years may be
// negative.
func addYearsClamped(t time.Time, years int) time.Time {
	return addMonthsClamped(t, years*12)
}

// daysInMonth returns the number of days in the given month, accounting for
// leap years. It relies on time.Date's normalisation deliberately: day 0 of
// month+1 is the last day of month.
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// floorDiv is integer division rounding towards negative infinity, unlike
// Go's / which truncates towards zero.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// floorMod is the remainder matching floorDiv: always in [0, b) for b > 0.
func floorMod(a, b int) int {
	return a - floorDiv(a, b)*b
}
