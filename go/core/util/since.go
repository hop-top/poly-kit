// Package util provides shared helpers for hop-top CLIs.
package util

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"github.com/tj/go-naturaldate"
)

// ParseSince parses a git --since/--after compatible string relative
// to time.Now(). See ParseSinceAt for supported formats.
func ParseSince(s string) (time.Time, error) {
	return ParseSinceAt(s, time.Now())
}

// ParseSinceAt parses a datetime string relative to the given reference
// time. Supported formats:
//
//   - "yesterday", "tomorrow"
//   - "N day(s)/week(s)/month(s)/year(s)/hour(s)/minute(s)/second(s) ago"
//   - Short relative: "Nd", "Nh", "Nm", "Nw", "NM", "Ny"
//   - Natural language: "next monday", "last week", "2 weeks ago"
//   - Month names: "May 1", "May 1 2026", "1 May 2026"
//   - ISO 8601 & variants: "2006-01-02", "2006-01-02T15:04:05Z",
//     "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04"
func ParseSinceAt(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("timeutil: empty input")
	}

	// named relative
	if s == "yesterday" {
		return now.AddDate(0, 0, -1), nil
	}

	// "N <unit> ago"
	if strings.HasSuffix(s, " ago") {
		return parseRelative(s, now)
	}

	// short relative: 7d, 24h, 30m, 2w, 3M, 1y
	if t, err := parseShort(s, now); err == nil {
		return t, nil
	}

	// natural language (next monday, last week, etc.) + ISO 8601
	return parseNatural(s, now)
}

func parseRelative(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSuffix(s, " ago")
	parts := strings.SplitN(s, " ", 2)
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("timeutil: invalid relative format %q", s+" ago")
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return time.Time{}, fmt.Errorf("timeutil: invalid count %q", parts[0])
	}
	unit := strings.TrimSuffix(parts[1], "s") // normalize plural
	switch unit {
	case "second":
		return now.Add(-time.Duration(n) * time.Second), nil
	case "minute":
		return now.Add(-time.Duration(n) * time.Minute), nil
	case "hour":
		return now.Add(-time.Duration(n) * time.Hour), nil
	case "day":
		return now.AddDate(0, 0, -n), nil
	case "week":
		return now.AddDate(0, 0, -n*7), nil
	case "month":
		return now.AddDate(0, -n, 0), nil
	case "year":
		return now.AddDate(-n, 0, 0), nil
	default:
		return time.Time{}, fmt.Errorf("timeutil: unknown unit %q", parts[1])
	}
}

// parseShort parses compact relative durations with single-char suffixes:
//
//   - s = seconds
//   - m = minutes (lowercase)
//   - h = hours
//   - d = days
//   - w = weeks
//   - M = months (uppercase; distinguishes from minutes)
//   - y = years
//
// Example: "7d" = 7 days ago, "3M" = 3 months ago, "30m" = 30 minutes ago.
func parseShort(s string, now time.Time) (time.Time, error) {
	if len(s) < 2 {
		return time.Time{}, fmt.Errorf("too short")
	}
	suffix := s[len(s)-1]
	numStr := s[:len(s)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return time.Time{}, fmt.Errorf("invalid")
	}
	switch suffix {
	case 's':
		return now.Add(-time.Duration(n) * time.Second), nil
	case 'm':
		return now.Add(-time.Duration(n) * time.Minute), nil
	case 'h':
		return now.Add(-time.Duration(n) * time.Hour), nil
	case 'd':
		return now.AddDate(0, 0, -n), nil
	case 'w':
		return now.AddDate(0, 0, -n*7), nil
	case 'M':
		return now.AddDate(0, -n, 0), nil
	case 'y':
		return now.AddDate(-n, 0, 0), nil
	default:
		return time.Time{}, fmt.Errorf("unknown suffix")
	}
}

var isoLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

func parseISO(s string) (time.Time, error) {
	for _, layout := range isoLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}

	// Only try dateparse for inputs that look date-like (contain numbers and common separators)
	if looksLikeDate(s) {
		if t, err := dateparse.ParseAny(s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("timeutil: unrecognized format %q", s)
}

// looksLikeDate checks if a string resembles a date/time phrase
func looksLikeDate(s string) bool {
	// Check for explicit date markers
	if looksLikeMonthName(s) || strings.ContainsAny(s, "-/:0123456789") {
		return true
	}
	// Check for relative time phrases
	relativePhrases := []string{
		"next ", "last ", "this ", "day", "week", "month", "year",
	}
	lower := strings.ToLower(s)
	for _, phrase := range relativePhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// looksLikeMonthName checks if string contains a month abbreviation (3+ consecutive letters)
func looksLikeMonthName(s string) bool {
	months := []string{
		"jan", "feb", "mar", "apr", "may", "jun",
		"jul", "aug", "sep", "oct", "nov", "dec",
		"january", "february", "march", "april", "june",
		"july", "august", "september", "october", "november", "december",
	}
	lower := strings.ToLower(s)
	for _, month := range months {
		if strings.Contains(lower, month) {
			return true
		}
	}
	return false
}

// parseNatural tries tj/go-naturaldate for relative expressions like
// "next monday", "last week", etc. Falls back to parseISO for absolute dates.
func parseNatural(s string, now time.Time) (time.Time, error) {
	// Only try naturaldate if it looks like a date phrase (contains spaces and date-like chars)
	if strings.Contains(s, " ") && looksLikeDate(s) {
		if t, err := naturaldate.Parse(s, now); err == nil {
			// naturaldate returns 'now' unchanged when it can't parse
			// so only accept if the result is meaningfully different
			if !t.Equal(now) {
				return t, nil
			}
		}
	}
	// Fallback to ISO/absolute date parsing
	return parseISO(s)
}
