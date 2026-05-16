package provenance

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Normalize canonicalises a URL string for cassette comparison. It is
// the contract surface between this package and the xrr cassette
// recorder: both ends apply the same normalisation so
// AssertProvenanceMatchesCassette becomes a string-compare problem,
// not a fuzzy-match problem.
//
// Normalisation rules:
//   - Lowercase scheme + host.
//   - Strip default ports (http/80, https/443).
//   - Sort query parameters by key, then value.
//   - Strip the URL fragment (cassettes never replay fragment-level
//     navigation).
//   - Preserve the path verbatim (cassettes are case-sensitive on path).
//
// Non-HTTP schemes ("doc://", "exec://", "sql://") flow through
// unchanged so adopters can pass their custom URLs without losing
// information. Only http/https get the full treatment.
func Normalize(rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("normalize: empty URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("normalize: parse %q: %w", rawURL, err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
		u.Scheme = scheme
		u.Host = canonicalHost(u.Host, scheme)
		u.Fragment = ""
		u.RawQuery = sortedQuery(u.Query())
		return u.String(), nil
	default:
		// Non-HTTP schemes: lowercase the scheme + opaque/host parts but
		// keep everything else intact.
		u.Scheme = scheme
		return u.String(), nil
	}
}

func canonicalHost(host, scheme string) string {
	h := strings.ToLower(host)
	switch scheme {
	case "http":
		h = strings.TrimSuffix(h, ":80")
	case "https":
		h = strings.TrimSuffix(h, ":443")
	}
	return h
}

func sortedQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		vals := append([]string(nil), values[k]...)
		sort.Strings(vals)
		for j, v := range vals {
			if i > 0 || j > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	return b.String()
}
