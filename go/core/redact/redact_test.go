package redact_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/redact"
)

func TestNew_EmptyAndMaskStrategy(t *testing.T) {
	r := redact.New()
	require.NotNil(t, r)
	// No rules → text passes through unchanged.
	assert.Equal(t, "hello world", r.Apply("hello world"))
	stats := r.Stats()
	assert.Equal(t, 0, stats.Rules)
	assert.Equal(t, uint64(0), stats.Matches)
}

func TestAddRule_BadRegexErrors(t *testing.T) {
	r := redact.New()
	_, err := r.AddRule("bad", "([", "X")
	assert.Error(t, err, "unclosed group should error at compile time")
}

func TestAddRule_AppendsAndAppliesMask(t *testing.T) {
	r, err := redact.New().AddRule("openai", `sk-[a-zA-Z0-9]{20,}`, "")
	require.NoError(t, err)
	out := r.Apply("OPENAI_API_KEY=sk-ABCDEFGHIJ1234567890XX trailing")
	assert.Equal(t, "OPENAI_API_KEY=***REDACTED*** trailing", out)
	assert.Equal(t, 1, r.Stats().Rules)
	assert.Equal(t, uint64(1), r.Stats().Matches)
}

func TestApplyBytes_SameAsApply(t *testing.T) {
	r, err := redact.New().AddRule("openai", `sk-[a-zA-Z0-9]{20,}`, "")
	require.NoError(t, err)
	in := []byte("token=sk-ABCDEFGHIJ1234567890XY done")
	got := r.ApplyBytes(in)
	assert.Equal(t, "token=***REDACTED*** done", string(got))
}

func TestScan_FindsWithoutReplacing(t *testing.T) {
	r, err := redact.New().AddRule("aws", `AKIA[0-9A-Z]{16}`, "")
	require.NoError(t, err)
	in := "use AKIAIOSFODNN7EXAMPLE here and AKIA1234567890ABCDEF"
	matches := r.Scan(in)
	require.Len(t, matches, 2)
	assert.Equal(t, "aws", matches[0].RuleID)
	assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", matches[0].Original)
	assert.True(t, strings.Contains(in[matches[0].Start:matches[0].End], "AKIAI"))
	// Scan must NOT increment Match counters.
	assert.Equal(t, uint64(0), r.Stats().Matches)
}

func TestAddRules_BulkAdd(t *testing.T) {
	rule1, err := redact.NewRule("a", `foo`, "")
	require.NoError(t, err)
	rule2, err := redact.NewRule("b", `bar`, "")
	require.NoError(t, err)
	r := redact.New().AddRules(rule1, rule2)
	assert.Equal(t, 2, r.Stats().Rules)
	out := r.Apply("foo bar baz")
	assert.Equal(t, "***REDACTED*** ***REDACTED*** baz", out)
}

func TestMatch_StartEnd(t *testing.T) {
	r, err := redact.New().AddRule("digits", `\d+`, "")
	require.NoError(t, err)
	matches := r.Scan("abc 123 def 4567")
	require.Len(t, matches, 2)
	assert.Equal(t, "123", matches[0].Original)
	assert.Equal(t, 4, matches[0].Start)
	assert.Equal(t, 7, matches[0].End)
	assert.Equal(t, "4567", matches[1].Original)
	assert.Equal(t, 12, matches[1].Start)
	assert.Equal(t, 16, matches[1].End)
}

func TestStats_ByRuleAttribution(t *testing.T) {
	// Use Tag strategy so the substituted text doesn't accidentally match a
	// later rule (Mask injects "***REDACTED***" which collides with [A-Z]+).
	r := redact.New()
	_, err := r.SetReplacement(redact.Tag)
	require.NoError(t, err)
	_, err = r.AddRule("d1", `\d+`, "")
	require.NoError(t, err)
	_, err = r.AddRule("u1", `[A-Z]{3}`, "")
	require.NoError(t, err)
	_ = r.Apply("ABC 12 def GHI 34")
	s := r.Stats()
	assert.Equal(t, uint64(4), s.Matches)
	assert.Equal(t, uint64(2), s.ByRule["d1"])
	assert.Equal(t, uint64(2), s.ByRule["u1"])
}
