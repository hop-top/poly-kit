package svcconfig

import "strings"

// EnvSegment spells one key segment the way an environment variable
// does: upper case, with dots and dashes as underscores.
func EnvSegment(s string) string {
	return strings.NewReplacer(".", "_", "-", "_").Replace(strings.ToUpper(s))
}

// EnvKey maps an environment variable onto the services.* key it
// sets, following the kit convention: <PREFIX>_ then the key upper
// cased, dots as underscores — MYTOOL_SERVICES_API_BODY_LIMIT_MAX_BYTES
// sets services.api.body_limit.max_bytes.
//
// Underscores are ambiguous inside a key, so the name is split against
// what is known, longest match first: a service in services (or all),
// then a registered block, then a key. A name that matches no service
// is a supervisor key (services.failure_policy); a remainder that
// matches no block is one of the service's own keys
// (services.api.insecure_remote). A name inside a block that matches
// none of its keys still maps, so validation can refuse the unknown
// key rather than the variable being ignored.
//
// ok is false when name is not a services variable of prefix.
func EnvKey(name, prefix string, services []string) (key string, ok bool) {
	rest, found := strings.CutPrefix(name, EnvSegment(prefix)+"_"+EnvSegment(Root)+"_")
	if !found || rest == "" {
		return "", false
	}
	scope, tail := longestSegment(rest, append(append([]string(nil), services...), Shared))
	if scope == "" {
		return Root + "." + strings.ToLower(rest), true
	}
	if tail == "" {
		return Root + "." + scope, true
	}
	var names []string
	for _, b := range blocks {
		names = append(names, b.Name)
	}
	if block, keyTail := longestSegment(tail, names); block != "" {
		return Key(scope, block, strings.ToLower(keyTail)), true
	}
	return Root + "." + scope + "." + strings.ToLower(tail), true
}

// longestSegment finds the longest candidate c for which s starts
// with EnvSegment(c)+"_", returning it and the remainder after the
// underscore. A candidate equal to the whole of s matches with an
// empty remainder.
func longestSegment(s string, candidates []string) (match, rest string) {
	for _, c := range candidates {
		seg := EnvSegment(c)
		if len(seg) <= len(match) {
			continue
		}
		if s == seg {
			match, rest = c, ""
			continue
		}
		if r, ok := strings.CutPrefix(s, seg+"_"); ok && r != "" {
			match, rest = c, r
		}
	}
	return match, rest
}
