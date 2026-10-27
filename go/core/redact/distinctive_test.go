package redact_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/redact"
)

// A distinctive rule's regex starts with a mandatory literal of at
// least 3 characters. Apply trusts it without the keyword gate or the
// entropy floor; generic rules keep both. Scan keeps gitleaks.

func TestDistinctive_Classification(t *testing.T) {
	for pattern, want := range map[string]bool{
		`ghp_[0-9a-zA-Z]{36}`:                       true,
		`\bxoxb-[0-9]{10}`:                          true,
		`(?i)\b(p8e-[a-z0-9]{32})`:                  true,
		`\b((?:A3T[A-Z0-9]|AKIA|ASIA)[A-Z2-7]{16})`: true,
		`(?:sk-ant-|sk-proj-)[a-z]{8}`:              true,
		`ab[0-9]{8}`:                                false,
		`[\w.-]{0,50}?(?:adafruit)=([a-z0-9]{32})`:  false,
		`(?:AKIA|ab)[0-9]{8}`:                       false,
		`[0-9a-f]{12}`:                              false,
		`(?:sk|rk)_live_[a-z]{8}`:                   true,
		`(?:ghu|ghs)_[0-9a-zA-Z]{36}`:               true,
		`[ab]cd[0-9]`:                               true,
		`(?:s|r)k[a-z]{8}`:                          false,
		`A{3}[0-9]`:                                 false,
	} {
		rule, err := redact.NewRule("r", pattern, "")
		require.NoError(t, err)
		assert.Equal(t, want, rule.Distinctive(), pattern)
	}
}

func TestDistinctive_ApplySkipsKeywordGate(t *testing.T) {
	r := specRedactor(t, redact.Tag, redact.RuleSpec{ID: "t", Regex: `tok_[0-9a-f]{8}`, Keywords: []string{"zzz"}})
	assert.Equal(t, "x <t>", r.Apply("x tok_0123abcd"))
	assert.Empty(t, r.Scan("x tok_0123abcd"), "detection keeps the keyword gate")
}

func TestDistinctive_ApplySkipsEntropyFloor(t *testing.T) {
	r := specRedactor(t, redact.Tag, redact.RuleSpec{ID: "t", Regex: `tok_(a{8})`, SecretGroup: 1, Entropy: 3})
	assert.Equal(t, "tok_<t>", r.Apply("tok_aaaaaaaa"))
	assert.Empty(t, r.Scan("tok_aaaaaaaa"))
}

func TestDistinctive_GenericKeepsGateAndEntropy(t *testing.T) {
	r := specRedactor(t, redact.Tag,
		redact.RuleSpec{ID: "g", Regex: `[0-9a-f]{12}`, Keywords: []string{"key"}},
		redact.RuleSpec{ID: "e", Regex: `[a-h]{8}`, Entropy: 3})
	assert.Equal(t, "0123456789ab abcdefgh", r.Apply("0123456789ab abcdefgh"))
}

func TestDistinctive_DefaultAirtablePatWithoutKeyword(t *testing.T) {
	r, err := redact.NewEmbedded()
	require.NoError(t, err)
	pat := "pat" + "Zq9Lm2Wx7Rt4Vb." + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	assert.NotContains(t, r.Apply("a "+pat+" b"), pat)
	assert.Empty(t, r.Scan("a "+pat+" b"))
}
