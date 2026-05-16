package util

import (
	"strings"
	"unicode"
)

// Slug converts s to a URL-safe slug: lowercase, alphanumeric + hyphens,
// no leading/trailing hyphens, no consecutive hyphens. Non-ASCII is stripped.
func Slug(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	prev := byte('-') // treat start as hyphen to suppress leading
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteByte(byte(unicode.ToLower(r)))
			prev = 'a'
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteByte(byte(r))
			prev = 'a'
		default:
			// any non-alnum ASCII becomes hyphen (collapse consecutive)
			if prev != '-' {
				b.WriteByte('-')
				prev = '-'
			}
		}
	}

	return strings.TrimRight(b.String(), "-")
}
