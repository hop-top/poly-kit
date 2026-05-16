package redact_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/redact"
)

// canonical gitleaks fixtures, each chosen to match a specific rule in the
// vendored corpus.
var canonicalSecrets = map[string]string{
	// openai-api-key wants either the proj/svcacct/admin shape or the
	// legacy `sk-XXXX...T3BlbkFJ...` shape. Use the latter (shorter).
	"openai":    "sk-" + strings.Repeat("a", 20) + "T3BlbkFJ" + strings.Repeat("b", 20),
	"aws":       "AKIAIOSFODNN7CLIENTX", // aws-access-token
	"anthropic": "sk-ant-api03-" + strings.Repeat("a", 93) + "AA",
}

func newDefault(t *testing.T) *redact.Redactor {
	t.Helper()
	r := redact.New()
	gl, err := redact.LoadGitleaks(redact.DefaultGitleaksPath())
	require.NoError(t, err)
	r.AddRules(gl...)
	return r
}

func TestStrategy_Mask(t *testing.T) {
	r := newDefault(t)
	for name, secret := range canonicalSecrets {
		out := r.Apply("X=" + secret + " Y")
		assert.Contains(t, out, "***REDACTED***", "Mask should sub %s", name)
		assert.NotContains(t, out, secret, "raw secret leaked for %s", name)
	}
}

func TestStrategy_Tag(t *testing.T) {
	r := newDefault(t)
	_, err := r.SetReplacement(redact.Tag)
	require.NoError(t, err)
	out := r.Apply("OPENAI=" + canonicalSecrets["openai"] + " ")
	assert.Contains(t, out, "<openai-api-key>",
		"Tag should produce rule-id wrapper, got: %s", out)
}

func TestStrategy_Hash(t *testing.T) {
	r := newDefault(t)
	_, err := r.SetReplacement(redact.Hash)
	require.NoError(t, err)
	secret := canonicalSecrets["aws"]
	out := r.Apply("KEY=" + secret)
	// Compute the expected sha256:<8hex> prefix the redactor emits.
	sum := sha256.Sum256([]byte(secret))
	want := "sha256:" + hex.EncodeToString(sum[:4])
	assert.Contains(t, out, want, "expected stable hash %q in %q", want, out)
	// Repeat run yields same hash → log correlation usable.
	out2 := r.Apply("KEY=" + secret)
	assert.Contains(t, out2, want)
}

func TestStrategy_Custom(t *testing.T) {
	r := newDefault(t)
	_, err := r.SetReplacement(redact.Custom, func(m redact.Match) string {
		return "[" + m.RuleID + ":" + strings.ToUpper(m.Original[:3]) + "...]"
	})
	require.NoError(t, err)
	out := r.Apply("KEY=AKIAIOSFODNN7CLIENTX")
	assert.Contains(t, out, "[aws-access-token:AKI...]", "got: %s", out)
}

func TestStrategy_CustomRequiresFn(t *testing.T) {
	r := redact.New()
	_, err := r.SetReplacement(redact.Custom)
	assert.Error(t, err, "Custom without fn should error")

	_, err = r.SetReplacement(redact.Custom, nil)
	assert.Error(t, err, "Custom with nil fn should error")
}

func TestStrategy_CustomPanicFallsBackToMask(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	_, err = r.SetReplacement(redact.Custom, func(_ redact.Match) string {
		panic("intentional")
	})
	require.NoError(t, err)
	// Must NOT crash; falls back to Mask.
	out := r.Apply("a 12 b")
	assert.Equal(t, "a ***REDACTED*** b", out)
}

func TestStrategy_TagHonoursRuleLocalReplacement(t *testing.T) {
	// Per-rule replacement template overrides the default <{rule-id}>
	// template under the Tag strategy. Loaded rules carry their template
	// (e.g. Presidio's "<EMAIL>"); user-added rules with a non-empty
	// replacement also win.
	r, err := redact.New().AddRule("custom-id", `\d+`, "<NUMERIC>")
	require.NoError(t, err)
	_, err = r.SetReplacement(redact.Tag)
	require.NoError(t, err)
	out := r.Apply("count: 42")
	assert.Equal(t, "count: <NUMERIC>", out)
}
