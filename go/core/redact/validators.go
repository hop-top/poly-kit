package redact

import "strings"

// Exported checksum and structure validators for PII shapes. They are
// pure functions over the matched text, meant for WithValidator: a
// shape-only regex finds candidates, the validator keeps the real
// identifiers. None of them is wired into the built-in rules, so
// Default() output does not depend on them.

// ibanLen is the total IBAN length per ISO 3166 country, from the
// SWIFT IBAN registry (ISO 13616-2).
var ibanLen = map[string]int{
	"AD": 24, "AE": 23, "AL": 28, "AT": 20, "AZ": 28, "BA": 20, "BE": 16,
	"BG": 22, "BH": 22, "BI": 27, "BR": 29, "BY": 28, "CH": 21, "CR": 22,
	"CY": 28, "CZ": 24, "DE": 22, "DJ": 27, "DK": 18, "DO": 28, "EE": 20,
	"EG": 29, "ES": 24, "FI": 18, "FK": 18, "FO": 18, "FR": 27, "GB": 22,
	"GE": 22, "GI": 23, "GL": 18, "GR": 27, "GT": 28, "HN": 28, "HR": 21,
	"HU": 28, "IE": 22, "IL": 23, "IQ": 23, "IS": 26, "IT": 27, "JO": 30,
	"KW": 30, "KZ": 20, "LB": 28, "LC": 32, "LI": 21, "LT": 20, "LU": 20,
	"LV": 21, "LY": 25, "MC": 27, "MD": 24, "ME": 22, "MK": 19, "MN": 20,
	"MR": 27, "MT": 31, "MU": 30, "NI": 28, "NL": 18, "NO": 15, "OM": 23,
	"PK": 24, "PL": 28, "PS": 29, "PT": 25, "QA": 29, "RO": 24, "RS": 22,
	"RU": 33, "SA": 24, "SC": 31, "SD": 18, "SE": 24, "SI": 19, "SK": 24,
	"SM": 27, "SO": 23, "ST": 25, "SV": 28, "TL": 23, "TN": 24, "TR": 26,
	"UA": 29, "VA": 22, "VG": 24, "XK": 20, "YE": 30,
}

// ibanAlias maps territory prefixes that are printed with a parent
// country's registry rules to that country: the French overseas
// departments and collectivities (FR, 27), the Crown dependencies (GB,
// 22) and the Aland Islands (FI, 18). The registry lists none of them
// as a country of its own. A territory IBAN is valid when its mod-97
// check holds under the printed prefix or under the parent's.
var ibanAlias = map[string]string{
	"GF": "FR", "GP": "FR", "MQ": "FR", "RE": "FR", "YT": "FR", "PM": "FR",
	"BL": "FR", "MF": "FR", "NC": "FR", "PF": "FR", "TF": "FR", "WF": "FR",
	"GG": "GB", "IM": "GB", "JE": "GB", "AX": "FI",
}

// ValidateIBAN reports whether s is a well-formed IBAN: a known
// country prefix, the registry length for that country, an
// alphanumeric BBAN and a valid ISO 7064 mod-97-10 check with check
// digits 02-98 (00, 01 and 99 are never issued). Territory prefixes in
// ibanAlias are held to their parent's length. Spaces are
// ignored and letters are case-insensitive, so the printed form
// "GB82 WEST 1234 5698 7654 32" passes.
func ValidateIBAN(s string) bool {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ':
			continue
		case c >= 'a' && c <= 'z':
			c -= 'a' - 'A'
		case (c < '0' || c > '9') && (c < 'A' || c > 'Z'):
			return false
		}
		b = append(b, c)
	}
	if len(b) < 4 || b[0] < 'A' || b[0] > 'Z' || b[1] < 'A' || b[1] > 'Z' ||
		b[2] < '0' || b[2] > '9' || b[3] < '0' || b[3] > '9' {
		return false
	}
	cc := string(b[:2])
	base := cc
	if p, ok := ibanAlias[cc]; ok {
		base = p
	}
	if n, ok := ibanLen[base]; !ok || n != len(b) {
		return false
	}
	if cd := int(b[2]-'0')*10 + int(b[3]-'0'); cd < 2 || cd > 98 {
		return false
	}
	if ibanMod97(b) {
		return true
	}
	if base != cc {
		return ibanMod97(append([]byte(base), b[2:]...))
	}
	return false
}

// ibanMod97 reports whether b (uppercase, prefix and check digits
// first) satisfies ISO 7064 mod-97-10: move the first four characters
// to the end, map letters to 10..35 and reduce incrementally.
func ibanMod97(b []byte) bool {
	rem := 0
	for _, c := range append(append(make([]byte, 0, len(b)), b[4:]...), b[:4]...) {
		if c >= 'A' {
			rem = (rem*100 + int(c-'A') + 10) % 97
		} else {
			rem = (rem*10 + int(c-'0')) % 97
		}
	}
	return rem == 1
}

const (
	phoneMinDigits     = 8  // national, or international with a 00 prefix
	phoneMinDigitsPlus = 7  // "+" form: real E.164 numbers such as +683 4002
	phoneMaxDigits     = 15 // E.164 maximum
)

// phoneDigits returns the digits of s with common formatting removed
// (spaces, hyphens, dots, one non-empty parenthesis pair per group)
// and whether a leading "+" was present. ok is false on any other
// character, a misplaced "+", or unbalanced or empty parentheses.
func phoneDigits(s string) (digits string, plus, ok bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "+") {
		plus, s = true, s[1:]
	}
	var b strings.Builder
	depth, inGroup := 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			b.WriteByte(c)
			inGroup++
		case c == ' ' || c == '-' || c == '.':
		case c == '(':
			if depth++; depth > 1 {
				return "", false, false
			}
			inGroup = 0
		case c == ')':
			if depth--; depth < 0 || inGroup == 0 {
				return "", false, false
			}
		default:
			return "", false, false
		}
	}
	return b.String(), plus, depth == 0
}

// international returns the E.164 digits of s when it is written
// internationally: a leading "+", or a single "00" prefix (stripped
// once). plus reports which. A country code never starts with 0, so
// "+0044", "+00..." and "000..." fail.
func international(s string) (d string, plus, ok bool) {
	d, plus, ok = phoneDigits(s)
	if !ok {
		return "", false, false
	}
	if !plus {
		if !strings.HasPrefix(d, "00") {
			return d, false, false
		}
		d = d[2:]
	}
	if d == "" || d[0] == '0' {
		return "", plus, false
	}
	return d, plus, true
}

func phoneLenOK(n int, plus bool) bool {
	min := phoneMinDigits
	if plus {
		min = phoneMinDigitsPlus
	}
	return n >= min && n <= phoneMaxDigits
}

// ValidatePhone reports whether s is a plausible phone number: only
// digits and common separators (space, hyphen, dot, non-empty
// parentheses) with an optional leading "+".
//
// Digit counts (E.164 allows at most 15): a number with a leading "+"
// needs 7 to 15 digits (+683 4002 is real); anything else needs 8 to
// 15. A single leading "00" international prefix is not counted, and
// "+00..." or "+0..." is rejected, as NormalizeE164 does.
func ValidatePhone(s string) bool {
	d, plus, ok := phoneDigits(s)
	if !ok {
		return false
	}
	if id, p, intl := international(s); intl {
		return phoneLenOK(len(id), p)
	} else if plus || strings.HasPrefix(d, "00") {
		return false
	}
	return phoneLenOK(len(d), false)
}

// NormalizeE164 returns s in E.164 form ("+" then the country code and
// subscriber number, no separators). The input must be international,
// a leading "+" or a single "00" prefix; a national number has no
// country code to infer, so it reports false. Digit counts follow
// ValidatePhone: 7 to 15 after "+", 8 to 15 after "00".
func NormalizeE164(s string) (string, bool) {
	d, plus, ok := international(s)
	if !ok || !phoneLenOK(len(d), plus) {
		return "", false
	}
	return "+" + d, true
}

// ValidateNIR reports whether s is a French social security number
// (NIR): 13 characters plus a 2-digit key, where key = 97 - (n mod 97)
// and n is the first 13 as a number. Corsican departments 2A and 2B
// count as 19 and 18 in n. Spaces and dots are ignored. The first digit
// must be 1 or 2 (sex), or 7 or 8 (temporary numbers), and the month
// 01-12, 20-42 or 50-99 (INSEE special-case ranges).
func ValidateNIR(s string) bool {
	b := make([]byte, 0, 15)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == ' ' || c == '.':
		case c >= 'a' && c <= 'z':
			b = append(b, c-('a'-'A'))
		default:
			b = append(b, c)
		}
	}
	if len(b) != 15 {
		return false
	}
	switch b[0] {
	case '1', '2', '7', '8':
	default:
		return false
	}
	for i, c := range b {
		corsica := i == 6 && (c == 'A' || c == 'B') && b[5] == '2'
		if (c < '0' || c > '9') && !corsica {
			return false
		}
	}
	if m := int(b[3]-'0')*10 + int(b[4]-'0'); m == 0 || m > 12 && m < 20 || m > 42 && m < 50 {
		return false
	}
	var n uint64
	for i := 0; i < 13; i++ {
		c := b[i]
		switch {
		case i == 5 && b[5] == '2' && (b[6] == 'A' || b[6] == 'B'):
			// Department 2A/2B: the pair counts as 19 or 18.
			if b[6] == 'A' {
				n = n*100 + 19
			} else {
				n = n*100 + 18
			}
			continue
		case i == 6 && (c == 'A' || c == 'B'):
			continue
		}
		n = n*10 + uint64(c-'0')
	}
	key := uint64(b[13]-'0')*10 + uint64(b[14]-'0')
	return key == 97-n%97
}

// ValidateSIN reports whether s is a Canadian Social Insurance Number:
// 9 digits (spaces and hyphens ignored) passing the Luhn check, not
// starting with 0 or 8 (0 is unassigned, 8 is not issued).
func ValidateSIN(s string) bool {
	var d [9]byte
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '-' {
			continue
		}
		if c < '0' || c > '9' || n == 9 {
			return false
		}
		d[n] = c - '0'
		n++
	}
	if n != 9 || d[0] == 0 || d[0] == 8 {
		return false
	}
	sum := 0
	for i, v := range d {
		x := int(v)
		if i%2 == 1 {
			if x *= 2; x > 9 {
				x -= 9
			}
		}
		sum += x
	}
	return sum%10 == 0
}
