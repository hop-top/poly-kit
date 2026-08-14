package config

import (
	"math"
	"strconv"
	"strings"
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
//	"9223372036854775808" -> string (integer-looking but out of int range;
//	                         see below)
//
// ".inf"/".nan" resolve to float because they are unambiguously numeric
// in every YAML version and round-trip through yaml.v3 as floats either
// way; treating them as strings would be the lossy choice. "1e3" is
// likewise float — strconv.ParseFloat accepts scientific notation and
// yaml.v3 agrees.
//
// An integer-looking token that does not fit in an int stays a string.
// Falling back to float64 would round it — "9223372036854775808" would
// land in the file as 9.223372036854776e+18, a different number than the
// one typed — and inference here is never allowed to be lossy. Float
// parsing therefore only applies to tokens actually spelled as floats
// (carrying '.', 'e' or 'E'). Very large float spellings such as "1e400"
// already stayed strings for the same reason: ParseFloat reports them
// out of range.
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
	// Only try float for tokens actually spelled as floats. Without this
	// gate an integer-looking token too large for int falls through to
	// ParseFloat and silently changes value: "9223372036854775808" would
	// be written back as 9.223372036854776e+18. Inference must never be
	// lossy, so an out-of-range integer stays the string the user typed —
	// the same call made for "0x1F" and, already, for "1e400".
	if hasFloatSyntax(s) {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return s
}

// hasFloatSyntax reports whether s is spelled as a float rather than a
// plain integer: it carries a decimal point or an exponent marker.
func hasFloatSyntax(s string) bool {
	return strings.ContainsAny(s, ".eE")
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
