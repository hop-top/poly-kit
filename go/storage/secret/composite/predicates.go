package composite

import (
	"regexp"
	"strings"
)

// HasPrefix returns a predicate that matches keys starting with prefix.
func HasPrefix(prefix string) func(string) bool {
	return func(k string) bool { return strings.HasPrefix(k, prefix) }
}

// HasSuffix returns a predicate that matches keys ending with suffix.
func HasSuffix(suffix string) func(string) bool {
	return func(k string) bool { return strings.HasSuffix(k, suffix) }
}

// AnyOf returns a predicate that matches keys exactly equal to one of
// the given names.
func AnyOf(names ...string) func(string) bool {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return func(k string) bool {
		_, ok := set[k]
		return ok
	}
}

// MatchRegexp returns a predicate that matches keys against the given
// compiled regular expression. Use regexp.MustCompile at call site for
// the panic-on-bad-pattern shorthand.
func MatchRegexp(re *regexp.Regexp) func(string) bool {
	return func(k string) bool { return re.MatchString(k) }
}

// Or returns a predicate that matches when any of the given predicates
// matches. nil predicates count as catch-alls (always match), matching
// the convention used by Member.Owns.
func Or(preds ...func(string) bool) func(string) bool {
	return func(k string) bool {
		for _, p := range preds {
			if p == nil || p(k) {
				return true
			}
		}
		return false
	}
}

// And returns a predicate that matches only when all of the given
// predicates match. nil predicates count as catch-alls (always match).
func And(preds ...func(string) bool) func(string) bool {
	return func(k string) bool {
		for _, p := range preds {
			if p != nil && !p(k) {
				return false
			}
		}
		return true
	}
}

// Not returns a predicate that inverts p. Not(nil) matches nothing
// (the inversion of catch-all).
func Not(p func(string) bool) func(string) bool {
	if p == nil {
		return func(string) bool { return false }
	}
	return func(k string) bool { return !p(k) }
}
