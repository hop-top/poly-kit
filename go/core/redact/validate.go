package redact

import "strings"

// plausible reports whether the match s[start:end] of a validated rule
// is a real finding. Rules without a validator always pass.
//
// credit-card: the regex is shape-only (13-19 digits, optional space or
// hyphen separators, no word boundary so a label joined by "_" still
// matches). Two checks cut the false positives that corrupt
// identifiers:
//
//   - Luhn: a real PAN satisfies the mod-10 check; ~90% of random
//     digit runs do not.
//   - token boundary: a run glued to alphanumerics, or inside a
//     UUID-shaped token, is part of an identifier, not a card. A label
//     joined by a hyphen or underscore ("card-4111...", "cc_4111...")
//     is a boundary: the number is still redacted.
func plausible(ruleID, s string, start, end int) bool {
	switch strings.TrimSuffix(ruleID, "-pii") {
	case "credit-card":
		return luhnValid(s[start:end]) && !insideToken(s, start, end)
	}
	return true
}

// luhnValid reports whether the digits of s (separators ignored) number
// at least 13 and pass the Luhn mod-10 check.
func luhnValid(s string) bool {
	sum, n, double := 0, 0, false
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c < '0' || c > '9' {
			continue
		}
		d := int(c - '0')
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
		n++
	}
	return n >= 13 && sum%10 == 0
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// uuidLen is the length of 8-4-4-4-12 hex groups with hyphens.
const uuidLen = 36

// insideToken reports whether s[start:end] is part of a larger token:
// an alphanumeric directly adjacent, or overlap with a UUID-shaped
// run of hex groups. A hyphen or underscore neighbor alone is not.
func insideToken(s string, start, end int) bool {
	if start > 0 && isAlnum(s[start-1]) {
		return true
	}
	if end < len(s) && isAlnum(s[end]) {
		return true
	}
	for u := max(0, start-(uuidLen-1)); u < end && u+uuidLen <= len(s); u++ {
		if uuidAt(s, u) {
			return true
		}
	}
	return false
}

// uuidAt reports whether s[i:i+36] is 8-4-4-4-12 hex groups.
func uuidAt(s string, i int) bool {
	for k := 0; k < uuidLen; k++ {
		c := s[i+k]
		if k == 8 || k == 13 || k == 18 || k == 23 {
			if c != '-' {
				return false
			}
		} else if !isHex(c) {
			return false
		}
	}
	return true
}
