package redact_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/redact"
)

// The substring allowlist let an attacker who influenced a secret's VALUE
// opt that secret out of redaction: any match merely containing an allowed
// substring passed through verbatim. These tests pin the exact-match
// replacement (AllowExact) against both field-observed bypass vectors.

// Vector 1 — embedded token. A live credential that happens to contain the
// allowlisted fixture prefix must still be redacted.
func TestAllowExact_EmbeddedTokenStillRedacted(t *testing.T) {
	r, err := redact.New().AddRule("openai", `sk-[a-zA-Z0-9]{20,}`, "")
	require.NoError(t, err)
	r.AllowExact("sk-test")

	out := r.Apply("Authorization: Bearer sk-testGENUINEPRODKEY1234567890abcdef")

	assert.NotContains(t, out, "GENUINEPRODKEY",
		"a live key embedding the allowlisted prefix must NOT pass through")
	assert.Contains(t, out, "***REDACTED***")
}

// Vector 2 — suffix injection. An attacker-controlled domain that extends
// an allowlisted fixture domain must not inherit its exemption.
func TestAllowExact_SuffixInjectionStillRedacted(t *testing.T) {
	r, err := redact.New().AddRule("email",
		`([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})`, "")
	require.NoError(t, err)
	r.AllowExact("noreply@example.com")

	out := r.Apply("victim@example.com.evil.tld")

	assert.NotContains(t, out, "evil.tld",
		"attacker-extended domain must NOT inherit the fixture exemption")
	assert.Contains(t, out, "***REDACTED***")
}

// The legitimate case still works: an exact fixture value passes through.
func TestAllowExact_ExactMatchPasses(t *testing.T) {
	r, err := redact.New().AddRule("aws", `AKIA[A-Z0-9]{16}`, "")
	require.NoError(t, err)
	r.AllowExact("AKIAIOSFODNN7EXAMPLE")

	out := r.Apply("docs use AKIAIOSFODNN7EXAMPLE and prod uses AKIAREALKEY012345678")

	assert.Contains(t, out, "AKIAIOSFODNN7EXAMPLE",
		"the documented fixture must pass through unchanged")
	assert.NotContains(t, out, "AKIAREALKEY012345678",
		"a real key must still be redacted")
}

// Field regression from the aps consumer: bare network-prefix entries such
// as "10." exempted any match containing them, because token charsets admit
// digits and dots. Exact matching removes the hazard.
func TestAllowExact_NetworkPrefixNoLongerExemptsTokens(t *testing.T) {
	r, err := redact.New().AddRule("bearer", `Bearer\s+[\w=~@.+/-]{8,}`, "")
	require.NoError(t, err)
	r.AllowExact("10.")

	out := r.Apply("Bearer abcdef10.0123456789abcdefgh")

	assert.NotContains(t, out, "0123456789abcdefgh",
		"a token containing '10.' must NOT escape redaction")
}

// Empty entries must never allowlist everything.
func TestAllowExact_EmptyEntryIgnored(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	r.AllowExact("")

	assert.Equal(t, "a ***REDACTED*** b", r.Apply("a 12 b"))
}

// Allowlisted matches must remain observable. Previously an exempted match
// fired no observer and bumped no counter, so a bypass was invisible to
// audit tooling.
func TestAllowExact_AllowedMatchIsAuditable(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	r.AllowExact("99")

	var allowed []redact.Match
	r.OnAllowed(func(m redact.Match) { allowed = append(allowed, m) })

	out := r.Apply("1 99 2")

	assert.Contains(t, out, "99", "exact-allowlisted value still passes through")
	require.Len(t, allowed, 1, "the exempted match must be reported to observers")
	assert.Equal(t, "99", allowed[0].Original)
	assert.Equal(t, "digits", allowed[0].RuleID)

	assert.Equal(t, uint64(1), r.Stats().Allowed,
		"exempted matches must be counted so bypasses are detectable")
}

// Exact matching applies to the []byte path too.
func TestAllowExact_ApplyBytes(t *testing.T) {
	r, err := redact.New().AddRule("openai", `sk-[a-zA-Z0-9]{20,}`, "")
	require.NoError(t, err)
	r.AllowExact("sk-test")

	out := string(r.ApplyBytes([]byte("sk-testGENUINEPRODKEY1234567890abcdef")))

	assert.NotContains(t, out, "GENUINEPRODKEY")
}

// Rule packs opt into exact semantics via allowlist_exact. The deprecated
// substring allowlist key keeps loading so existing packs are unaffected.
func TestAllowExact_LoadedFromTOML(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "pii.toml")
	body := `
[[rule]]
id = "test-email"
pattern = '''([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})'''
replacement = "<EMAIL>"
allowlist_exact = ["noreply@example.com"]
`
	require.NoError(t, os.WriteFile(tmp, []byte(body), 0o644))
	rules, err := redact.LoadPresidio(tmp)
	require.NoError(t, err)
	r := redact.New().AddRules(rules...)

	out := r.Apply("noreply@example.com victim@example.com.evil.tld")

	assert.Contains(t, out, "noreply@example.com",
		"the exact fixture address passes through")
	assert.NotContains(t, out, "evil.tld",
		"an extended domain must not inherit the exemption")
}

// Scan reports what WOULD be redacted; exempted matches must be excluded
// from findings but still not leak via a substring bypass.
func TestAllowExact_Scan(t *testing.T) {
	r, err := redact.New().AddRule("openai", `sk-[a-zA-Z0-9]{20,}`, "")
	require.NoError(t, err)
	r.AllowExact("sk-testabcdefghij1234567890")

	found := r.Scan("sk-testabcdefghij1234567890 sk-testGENUINEPRODKEY1234567890")

	require.Len(t, found, 1, "only the non-exempt secret should be reported")
	assert.Contains(t, found[0].Original, "GENUINEPRODKEY")
}
