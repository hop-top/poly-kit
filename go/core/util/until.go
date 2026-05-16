package util

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tj/go-naturaldate"
)

// weekdays maps lowercase weekday names to time.Weekday values.
var weekdays = map[string]time.Weekday{
	"sunday":    time.Sunday,
	"monday":    time.Monday,
	"tuesday":   time.Tuesday,
	"wednesday": time.Wednesday,
	"thursday":  time.Thursday,
	"friday":    time.Friday,
	"saturday":  time.Saturday,
}

// ParseUntil parses a forward-looking date string relative to
// time.Now(). See ParseUntilAt for supported formats.
func ParseUntil(s string) (time.Time, error) {
	return ParseUntilAt(s, time.Now())
}

// ParseUntilAt parses a forward-looking date string relative to the
// given reference time. Supported formats:
//
//   - "tomorrow"
//   - "in N day(s)/week(s)/month(s)/year(s)/hour(s)/minute(s)/second(s)"
//   - Short relative: "+Nd", "+Nh", "+Nm", "+Nw", "+NM", "+Ny"
//   - Weekday names: "monday", "friday" (next occurrence)
//   - Natural language: "next monday", "next week"
//   - Month names: "May 1", "May 1 2026", "1 May 2026"
//   - ISO 8601 & variants: "2006-01-02", "2006-01-02T15:04:05Z",
//     "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04"
func ParseUntilAt(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("timeutil: empty input")
	}

	if s == "tomorrow" {
		return now.AddDate(0, 0, 1), nil
	}

	// "in N <unit>"
	if strings.HasPrefix(s, "in ") {
		return parseForward(s, now)
	}

	// "+3d", "+24h", etc.
	if strings.HasPrefix(s, "+") {
		if t, err := parseShortForward(s, now); err == nil {
			return t, nil
		}
	}

	// weekday name
	if t, err := parseNextWeekday(s, now); err == nil {
		return t, nil
	}

	// natural language (next monday, next week, etc.) + ISO 8601
	return parseNaturalForward(s, now)
}

// parseNaturalForward tries tj/go-naturaldate for relative expressions like
// "next monday", "next week", etc. Falls back to parseISO for absolute dates.
func parseNaturalForward(s string, now time.Time) (time.Time, error) {
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

func parseForward(s string, now time.Time) (time.Time, error) {
	s = strings.TrimPrefix(s, "in ")
	parts := strings.SplitN(s, " ", 2)
	if len(parts) != 2 {
		return time.Time{},
			fmt.Errorf("timeutil: invalid forward format %q", "in "+s)
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return time.Time{},
			fmt.Errorf("timeutil: invalid count %q", parts[0])
	}
	unit := strings.TrimSuffix(parts[1], "s")
	switch unit {
	case "second":
		return now.Add(time.Duration(n) * time.Second), nil
	case "minute":
		return now.Add(time.Duration(n) * time.Minute), nil
	case "hour":
		return now.Add(time.Duration(n) * time.Hour), nil
	case "day":
		return now.AddDate(0, 0, n), nil
	case "week":
		return now.AddDate(0, 0, n*7), nil
	case "month":
		return now.AddDate(0, n, 0), nil
	case "year":
		return now.AddDate(n, 0, 0), nil
	default:
		return time.Time{},
			fmt.Errorf("timeutil: unknown unit %q", parts[1])
	}
}

// parseShortForward parses "+Nd", "+Nh", etc.
func parseShortForward(s string, now time.Time) (time.Time, error) {
	s = strings.TrimPrefix(s, "+")
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
		return now.Add(time.Duration(n) * time.Second), nil
	case 'm':
		return now.Add(time.Duration(n) * time.Minute), nil
	case 'h':
		return now.Add(time.Duration(n) * time.Hour), nil
	case 'd':
		return now.AddDate(0, 0, n), nil
	case 'w':
		return now.AddDate(0, 0, n*7), nil
	case 'M':
		return now.AddDate(0, n, 0), nil
	case 'y':
		return now.AddDate(n, 0, 0), nil
	default:
		return time.Time{}, fmt.Errorf("unknown suffix")
	}
}

// parseNextWeekday returns the next occurrence of the named weekday.
func parseNextWeekday(s string, now time.Time) (time.Time, error) {
	target, ok := weekdays[strings.ToLower(s)]
	if !ok {
		return time.Time{},
			fmt.Errorf("timeutil: not a weekday %q", s)
	}
	current := now.Weekday()
	days := int(target - current)
	if days <= 0 {
		days += 7
	}
	return now.AddDate(0, 0, days), nil
}
