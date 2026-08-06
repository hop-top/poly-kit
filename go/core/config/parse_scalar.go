package config

import (
	"math"
	"strconv"
)

// ParseScalar converts a raw command-line string argument into a typed Go
// value suitable for [SetValue], so that `mycli config set threshold 0.9`
// writes `threshold: 0.9` rather than `threshold: "0.9"`.
//
// Inference is deliberately narrow — int, float, bool and null only —
// and is implemented with strconv rather than YAML resolution, so the
// accepted grammar is explicit and stable across yaml library versions.
// Everything else stays a string:
//
//	"123"            -> int(123)
//	"0.9", "1e3"     -> float64
//	".inf", ".nan"   -> float64 (see below)
//	"true", "false"  -> bool (exact lowercase only)
//	"null", "~", "Null", "NULL" -> nil
//	"yes"/"no"/"on"/"off" -> string (YAML 1.1 lookalikes; yaml.v3 reads
//	                         them back as strings, so keep them strings)
//	"0x1F", "0o17"   -> string (strconv would accept these as ints; a CLI
//	                    arg spelled in hex is far more likely a literal
//	                    token — an ID, a color, a mask — than a number)
//	"2024-01-01"     -> string (never a timestamp: lossy and surprising)
//	"abc", ""        -> string
//
// ".inf"/".nan" resolve to float because they are unambiguously numeric
// in every YAML version and round-trip through yaml.v3 as floats either
// way; treating them as strings would be the lossy choice. "1e3" is
// likewise float — strconv.ParseFloat accepts scientific notation and
// yaml.v3 agrees.
//
// Leading/trailing whitespace is significant and prevents inference: a
// value is only converted when the entire string parses.
func ParseScalar(s string) any {
	switch s {
	case "":
		return ""
	case "true", "false":
		return s == "true"
	case "null", "Null", "NULL", "~":
		return nil
	case ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF":
		return math.Inf(1)
	case "-.inf", "-.Inf", "-.INF":
		return math.Inf(-1)
	case ".nan", ".NaN", ".NAN":
		return math.NaN()
	}

	// Reject non-decimal integer spellings (0x, 0o, 0b) and anything with
	// an underscore separator before handing off to strconv, which would
	// otherwise accept them for base 0 / Go literal syntax.
	if !decimalNumeric(s) {
		return s
	}

	if i, err := strconv.Atoi(s); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// decimalNumeric reports whether s is built only from characters that can
// appear in a plain decimal integer or float literal. It is a gate, not a
// validator: strconv still decides.
func decimalNumeric(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r == '+' || r == '-' || r == '.' || r == 'e' || r == 'E':
		default:
			return false
		}
	}
	return true
}
