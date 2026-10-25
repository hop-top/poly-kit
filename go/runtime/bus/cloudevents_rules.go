package bus

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// errBadTime is the cause behind "time: invalid RFC 3339 timestamp"
// when the grammar check, not time.Parse, rejects the text.
var errBadTime = errors.New("not RFC 3339 date-time")

// parseCETime parses a CloudEvents time attribute against RFC 3339
// §5.6 date-time with kit's rulings:
//
//	YYYY-MM-DD ("T"|"t") hh:mm:ss ["." 1-9 digits] ("Z"|"z"|("+"|"-")hh:mm)
//
// The year has 4 digits (0000-9999). The fraction separator is "."
// only. Seconds run 00-59: leap second 60 is rejected because
// time.Time cannot hold it. Month 01-12, day valid for the month, hour
// 00-23, minute 00-59, offset hour 00-23, offset minute 00-59.
func parseCETime(s string) (time.Time, error) {
	if !validCETimeGrammar(s) {
		return time.Time{}, errBadTime
	}
	b := []byte(s)
	b[10] = 'T'
	if last := len(b) - 1; b[last] == 'z' {
		b[last] = 'Z'
	}
	return time.Parse(time.RFC3339Nano, string(b))
}

func validCETimeGrammar(s string) bool {
	if len(s) < 20 {
		return false
	}
	num := func(i, n int) (int, bool) {
		v := 0
		for k := i; k < i+n; k++ {
			if s[k] < '0' || s[k] > '9' {
				return 0, false
			}
			v = v*10 + int(s[k]-'0')
		}
		return v, true
	}
	_, okY := num(0, 4)
	mo, okMo := num(5, 2)
	d, okD := num(8, 2)
	h, okH := num(11, 2)
	mi, okMi := num(14, 2)
	sec, okS := num(17, 2)
	if !okY || !okMo || !okD || !okH || !okMi || !okS ||
		s[4] != '-' || s[7] != '-' || (s[10] != 'T' && s[10] != 't') ||
		s[13] != ':' || s[16] != ':' {
		return false
	}
	if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || sec > 59 {
		return false
	}
	i := 19
	if s[i] == '.' {
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if n := j - i - 1; n < 1 || n > 9 {
			return false
		}
		i = j
	}
	rest := s[i:]
	switch {
	case rest == "Z" || rest == "z":
		return true
	case len(rest) == 6 && (rest[0] == '+' || rest[0] == '-') && rest[3] == ':':
		oh, ok1 := num(i+1, 2)
		om, ok2 := num(i+4, 2)
		return ok1 && ok2 && oh <= 23 && om <= 59
	}
	return false
}

// ceTimeEncodable reports whether t, in UTC, has a 4-digit year, the
// range the time grammar can carry.
func ceTimeEncodable(t time.Time) bool {
	y := t.UTC().Year()
	return y >= 0 && y <= 9999
}

// isJSONMediaType reports whether ct names JSON per the CloudEvents JSON
// format §3.1: subtype "json" or a "+json" suffix, parameters ignored,
// case-insensitive.
func isJSONMediaType(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	slash := strings.IndexByte(ct, '/')
	if slash <= 0 {
		return false
	}
	sub := ct[slash+1:]
	return sub == "json" || strings.HasSuffix(sub, "+json")
}

// decodeCEBase64 decodes data_base64: RFC 4648 standard alphabet,
// padding required, canonical trailing bits, no CR or LF anywhere.
func decodeCEBase64(s string) ([]byte, error) {
	if strings.ContainsAny(s, "\r\n") {
		return nil, errors.New("line break in base64")
	}
	return base64.StdEncoding.Strict().DecodeString(s)
}
