package redact_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"hop.top/kit/go/core/redact"
)

// Apply redacts; it does not filter false positives the way a repo
// scanner does. Stopwords never exempt a secret from Apply, and an
// allowlist regex exempts only when it matches the whole target.
// Scan keeps gitleaks' detection semantics.

func failClosedRule(a redact.AllowlistSpec) redact.RuleSpec {
	return redact.RuleSpec{ID: "k", Regex: `key=([A-Za-z0-9]{8})`, SecretGroup: 1,
		Allowlists: []redact.AllowlistSpec{a}}
}

func TestFailClosed_StopwordDoesNotExemptApply(t *testing.T) {
	r := specRedactor(t, redact.Tag, failClosedRule(redact.AllowlistSpec{StopWords: []string{"test"}}))
	in := "key=Zq9TEST7"
	assert.Equal(t, "key=<k>", r.Apply(in), "a secret containing a stopword is still redacted")
	assert.Equal(t, []redact.Span{{4, 12, "k"}}, r.Spans(in))
	assert.Empty(t, r.Scan(in), "detection keeps the gitleaks stopword exemption")
}

func TestFailClosed_PartialRegexDoesNotExemptApply(t *testing.T) {
	r := specRedactor(t, redact.Tag, failClosedRule(redact.AllowlistSpec{Regexes: []string{`^EXAMPL`}}))
	in := "key=EXAMPLzz"
	assert.Equal(t, "key=<k>", r.Apply(in), "a regex matching part of the secret does not exempt it")
	assert.Empty(t, r.Scan(in), "detection keeps gitleaks' substring regex semantics")
}

func TestFailClosed_WholeSecretRegexExemptsApply(t *testing.T) {
	r := specRedactor(t, redact.Tag, failClosedRule(redact.AllowlistSpec{Regexes: []string{`EXAMPL[A-Z]{2}`}}))
	assert.Equal(t, "key=EXAMPLZZ", r.Apply("key=EXAMPLZZ"))
	assert.Equal(t, "key=<k>", r.Apply("key=EXAMPLZz"))
}

func TestFailClosed_AndWithStopwordNeverExemptsApply(t *testing.T) {
	r := specRedactor(t, redact.Tag, failClosedRule(redact.AllowlistSpec{
		Condition: "AND", Regexes: []string{`.+`}, StopWords: []string{"zz"}}))
	assert.Equal(t, "key=<k>", r.Apply("key=abczz123"))
	assert.Empty(t, r.Scan("key=abczz123"))
}

func TestFailClosed_DefaultPolicyStopwordStillRedacted(t *testing.T) {
	r, err := redact.NewEmbedded()
	assert.NoError(t, err)
	// generic-api-key carries upstream stopwords; "client" is one.
	in := "api_token = " + "Zq9Lm2client7Rt4Vb8"
	assert.NotContains(t, r.Apply(in), "Zq9Lm2client7Rt4Vb8")
}
