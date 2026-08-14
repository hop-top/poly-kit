package redact_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/redact"
)

func TestAllowExact_GlobalLiteralPasses(t *testing.T) {
	r, err := redact.New().AddRule("openai", `sk-[a-zA-Z0-9]{20,}`, "")
	require.NoError(t, err)
	r.AllowExact("sk-testabcdefghij1234567890")

	out := r.Apply("dev=sk-testabcdefghij1234567890 prod=sk-realabcdefghij1234567890")
	assert.Contains(t, out, "sk-testabcdefghij1234567890",
		"the exact dev fixture should pass through")
	assert.NotContains(t, out, "sk-realabcdefghij1234567890",
		"production-shape secret should still be redacted")
	assert.Contains(t, out, "***REDACTED***")
}

func TestAllowExact_NoRedactionObserverFireForAllowedMatches(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	r.AllowExact("99")

	var seen []redact.Match
	r.OnMatch(func(m redact.Match) { seen = append(seen, m) })

	_ = r.Apply("1 2 99 3")
	assert.Len(t, seen, 3, "OnMatch should NOT fire for the allowlisted '99'")
	for _, m := range seen {
		assert.NotEqual(t, "99", m.Original)
	}
}

func TestAllowExact_DoesNotIncrementMatchStats(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	r.AllowExact("9")

	_ = r.Apply("1 9 2 9 3")
	s := r.Stats()
	assert.Equal(t, uint64(3), s.Matches, "allowlisted matches must not bump Matches")
	assert.Equal(t, uint64(2), s.Allowed, "they are counted as Allowed instead")
}

func TestAllowExact_PerRuleAllowlist(t *testing.T) {
	r, err := redact.New().AddRule("email",
		`([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})`, "")
	require.NoError(t, err)
	r.AllowExact("noreply@example.com")

	out := r.Apply("noreply@example.com vs leak@real-corp.com")
	assert.Contains(t, out, "noreply@example.com", "exact fixture should pass")
	assert.NotContains(t, out, "leak@real-corp.com", "real address must be redacted")
	assert.Contains(t, out, "***REDACTED***")
}

func TestAllowExact_EmptyEntryAllowlistsNothing(t *testing.T) {
	// An empty entry must never exempt every match.
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	r.AllowExact("")

	out := r.Apply("a 12 b")
	assert.Equal(t, "a ***REDACTED*** b", out)
}

func TestAllowExact_PresidioRuleScopedAllowlistApplies(t *testing.T) {
	// Rule packs carry per-rule exemptions via allowlist_exact.
	tmp := filepath.Join(t.TempDir(), "pii.toml")
	body := `
[[rule]]
id = "test-email"
pattern = '''([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})'''
replacement = "<EMAIL>"
allowlist_exact = ["a@example.com", "c@test.local"]
`
	require.NoError(t, os.WriteFile(tmp, []byte(body), 0o644))
	rules, err := redact.LoadPresidio(tmp)
	require.NoError(t, err)
	r := redact.New().AddRules(rules...)

	out := r.Apply("a@example.com b@real.com c@test.local")
	assert.Contains(t, out, "a@example.com")
	assert.Contains(t, out, "c@test.local")
	assert.NotContains(t, out, "b@real.com")
}

// The removed substring allowlist matched inside a rule's match, so a
// secret embedding an entry escaped redaction. Pin that it cannot return:
// a rule pack using the old `allowlist` key must not exempt anything.
func TestLoadPresidio_LegacyAllowlistKeyIsInert(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "pii.toml")
	body := `
[[rule]]
id = "test-email"
pattern = '''([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})'''
replacement = "<EMAIL>"
allowlist = ["@example.com"]
`
	require.NoError(t, os.WriteFile(tmp, []byte(body), 0o644))
	rules, err := redact.LoadPresidio(tmp)
	require.NoError(t, err)
	r := redact.New().AddRules(rules...)

	out := r.Apply("victim@example.com.evil.tld")
	assert.NotContains(t, out, "evil.tld",
		"the legacy substring key must no longer exempt anything")
}
