package redact_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/redact"
)

func TestLoadGitleaks_LoadsManyRules(t *testing.T) {
	rules, err := redact.LoadGitleaks(redact.DefaultGitleaksPath())
	require.NoError(t, err)
	// Plan minimum: >= 100 rules survive RE2 compilation.
	assert.GreaterOrEqual(t, len(rules), 100, "expected at least 100 gitleaks rules to load")
}

func TestLoadGitleaks_CanonicalPatternsRedact(t *testing.T) {
	rules, err := redact.LoadGitleaks(redact.DefaultGitleaksPath())
	require.NoError(t, err)
	r := redact.New().AddRules(rules...)
	_, err = r.SetReplacement(redact.Tag)
	require.NoError(t, err)

	// AWS access token canonical fixture.
	out := r.Apply("export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7CLIENTX")
	assert.Contains(t, strings.ToLower(out), "<aws-access-token>",
		"expected aws-access-token tag in: %s", out)

	// Anthropic API key canonical fixture.
	apiKey := "sk-ant-api03-" + strings.Repeat("a", 93) + "AA"
	out = r.Apply("ANTHROPIC=" + apiKey)
	assert.Contains(t, strings.ToLower(out), "<anthropic-api-key>",
		"expected anthropic-api-key tag in: %s", out)
}

func TestDefault_LoadsGitleaks(t *testing.T) {
	r := redact.Default()
	require.NotNil(t, r)
	// Default() must have rules loaded eagerly (lazily on first use).
	stats := r.Stats()
	assert.Greater(t, stats.Rules, 100, "Default() should have loaded >100 rules")
}

func TestLoadPresidio_LoadsRules(t *testing.T) {
	rules, err := redact.LoadPresidio(redact.DefaultPresidioPath())
	require.NoError(t, err)
	// v1 coverage target: ~10 rules.
	assert.GreaterOrEqual(t, len(rules), 10, "expected at least 10 Presidio PII rules")
}

func TestLoadPresidio_RedactsCanonical(t *testing.T) {
	rules, err := redact.LoadPresidio(redact.DefaultPresidioPath())
	require.NoError(t, err)
	r := redact.New().AddRules(rules...)
	_, err = r.SetReplacement(redact.Tag)
	require.NoError(t, err)

	cases := map[string]string{
		"Email me at jad@example.com": "<EMAIL>",
		"Call +14155552671 anytime":   "<PHONE>",
		"SSN 123-45-6789":             "<SSN>",
		"My IP is 192.168.1.42":       "<IP>",
	}
	for in, want := range cases {
		out := r.Apply(in)
		assert.Contains(t, out, want, "input %q produced %q", in, out)
	}
}

func TestDefault_HasPresidioRules(t *testing.T) {
	// Force a fresh singleton for this assertion via the public API only.
	r := redact.Default()
	_, err := r.SetReplacement(redact.Tag)
	require.NoError(t, err)
	out := r.Apply("contact: dev@example.com")
	assert.Contains(t, out, "<EMAIL>", "Default() should redact emails after Presidio load")
}

func TestDefaultPresidioPath_HonoursEnv(t *testing.T) {
	t.Setenv("KIT_REDACT_PII_RULES_PATH", "/tmp/custom-pii.toml")
	assert.Equal(t, "/tmp/custom-pii.toml", redact.DefaultPresidioPath())
}

func TestDefaultGitleaksPath_HonoursEnvOverride(t *testing.T) {
	t.Setenv("KIT_REDACT_RULES_PATH", "/custom/path/rules.toml")
	assert.Equal(t, "/custom/path/rules.toml", redact.DefaultGitleaksPath())
}

func TestDefaultGitleaksPath_DefaultEmpty(t *testing.T) {
	t.Setenv("KIT_REDACT_RULES_PATH", "")
	// Returns the embed sentinel "embed:" when env unset; loader handles it.
	got := redact.DefaultGitleaksPath()
	assert.NotEmpty(t, got)
}
