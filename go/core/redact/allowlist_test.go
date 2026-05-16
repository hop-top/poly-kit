package redact_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/redact"
)

func TestAllow_GlobalSubstringPasses(t *testing.T) {
	r, err := redact.New().AddRule("openai", `sk-[a-zA-Z0-9]{20,}`, "")
	require.NoError(t, err)
	r.Allow("sk-test")

	out := r.Apply("dev=sk-testabcdefghij1234567890 prod=sk-realabcdefghij1234567890")
	assert.Contains(t, out, "sk-testabcdefghij1234567890",
		"sk-test substring should let dev fixture pass through")
	assert.NotContains(t, out, "sk-realabcdefghij1234567890",
		"production-shape secret should still be redacted")
	assert.Contains(t, out, "***REDACTED***")
}

func TestAllow_NoObserverFireForAllowedMatches(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	r.Allow("9")

	var seen []redact.Match
	r.OnMatch(func(m redact.Match) { seen = append(seen, m) })

	_ = r.Apply("1 2 99 3")
	assert.Len(t, seen, 3, "observer should NOT fire for the allowlisted '99'")
	for _, m := range seen {
		assert.NotContains(t, m.Original, "9")
	}
}

func TestAllow_DoesNotIncrementStats(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	r.Allow("9")

	_ = r.Apply("1 9 2 9 3")
	s := r.Stats()
	assert.Equal(t, uint64(3), s.Matches, "allowlisted matches must not bump Stats")
}

func TestAllow_PerRuleAllowlistFromTOML(t *testing.T) {
	// Per-rule allowlist arrives via the loader (Presidio TOML supports
	// allowlist = [...] per rule). Use Presidio's email rule and add an
	// allowlist to the loaded copy via a hand-built Rule with the same
	// pattern.
	r, err := redact.New().AddRule("email", `([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})`, "")
	require.NoError(t, err)
	// Presidio allowlist mechanism is exercised via the LoadPresidio path;
	// here we round-trip through the public global Allow() to assert the
	// substring-match contract holds end-to-end.
	r.Allow("@example.com")

	out := r.Apply("noreply@example.com vs leak@real-corp.com")
	assert.Contains(t, out, "noreply@example.com", "example.com fixture should pass")
	assert.NotContains(t, out, "leak@real-corp.com", "real address must be redacted")
	assert.Contains(t, out, "***REDACTED***")
}

func TestAllow_EmptySubstringIgnored(t *testing.T) {
	// Empty substring would otherwise allowlist EVERYTHING (every string
	// 'contains' the empty string). Defensive: skip empty entries.
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	r.Allow("")

	out := r.Apply("a 12 b")
	assert.Equal(t, "a ***REDACTED*** b", out)
}

func TestAllow_PresidioRuleScopedAllowlistApplies(t *testing.T) {
	// Verify that loading Presidio rules whose TOML includes an
	// allowlist field results in matching substrings being passed
	// through. Construct a tiny Presidio-shaped TOML and load it via
	// LoadPresidio with a temp file.
	tmp := filepath.Join(t.TempDir(), "pii.toml")
	body := `
[[rule]]
id = "test-email"
pattern = '''([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})'''
replacement = "<EMAIL>"
allowlist = ["@example.com", "@test.local"]
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
