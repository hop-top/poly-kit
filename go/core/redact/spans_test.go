package redact_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/redact"
)

func specRedactor(t *testing.T, strategy redact.Replacement, specs ...redact.RuleSpec) *redact.Redactor {
	t.Helper()
	r, err := redact.NewFromPolicy(redact.Policy{Rules: specs})
	require.NoError(t, err)
	_, err = r.SetReplacement(strategy)
	require.NoError(t, err)
	return r
}

// Overlapping spans merge into one region; the earliest start wins,
// then the longest, then the rule added first.
func TestSpans_OverlapMerge(t *testing.T) {
	r := specRedactor(t, redact.Tag,
		redact.RuleSpec{ID: "short", Regex: `abcd`},
		redact.RuleSpec{ID: "long", Regex: `abcdef`},
		redact.RuleSpec{ID: "tail", Regex: `defgh`},
	)
	assert.Equal(t, []redact.Span{{Start: 2, End: 10, RuleID: "long"}}, r.Spans("x abcdefgh y"))
	assert.Equal(t, "x <long> y", r.Apply("x abcdefgh y"))
}

func TestSpans_AdjacentMerge(t *testing.T) {
	r := specRedactor(t, redact.Tag,
		redact.RuleSpec{ID: "a", Regex: `A{3}`},
		redact.RuleSpec{ID: "b", Regex: `B{3}`},
	)
	assert.Equal(t, "<a> <b>", r.Apply("AAABBB BBB"))
	assert.Equal(t, []redact.Span{{0, 6, "a"}, {7, 10, "b"}}, r.Spans("AAABBB BBB"))
}

func TestSpans_TieGoesToFirstRule(t *testing.T) {
	r := specRedactor(t, redact.Tag,
		redact.RuleSpec{ID: "one", Regex: `k[0-9]{4}`},
		redact.RuleSpec{ID: "two", Regex: `k[0-9]+`},
	)
	assert.Equal(t, "<one>", r.Apply("k1234"))
}

// The reported leak: per-rule replacement let a later rule match the
// placeholder and a credentials file kept its secret key.
func TestApply_CredentialsFileFullyRedactedUnderTag(t *testing.T) {
	r, err := redact.NewEmbedded()
	require.NoError(t, err)
	_, err = r.SetReplacement(redact.Tag)
	require.NoError(t, err)
	keyID := "AKIA" + "QX7MZ2LP4RT6VB3N"
	secret := "wJalrXUtnFEMI" + "/K7MDENG/bPxRfiCYzq8Lw3Hd"
	in := "[default]\naws_access_key_id = " + keyID + "\naws_secret_access_key = " + secret + "\n"
	out := r.Apply(in)
	assert.NotContains(t, out, keyID)
	assert.NotContains(t, out, secret[13:])
	assert.Contains(t, out, "[default]\naws_access_key_id = <aws-access-token>\n")
}

func TestSecretGroup_RedactsOnlyTheGroup(t *testing.T) {
	r := specRedactor(t, redact.Mask,
		redact.RuleSpec{ID: "kv", Regex: `token=([a-z0-9]{8})(?:;|$)`, SecretGroup: 1})
	assert.Equal(t, "token=***REDACTED***; rest", r.Apply("token=ab12cd34; rest"))
	got := r.Scan("token=ab12cd34; rest")
	require.Len(t, got, 1)
	assert.Equal(t, redact.Match{RuleID: "kv", Original: "ab12cd34", Start: 6, End: 14}, got[0])
}

func TestSecretGroup_ZeroRedactsWholeMatch(t *testing.T) {
	r := specRedactor(t, redact.Mask, redact.RuleSpec{ID: "kv", Regex: `token=([a-z0-9]{8})`})
	assert.Equal(t, "***REDACTED*** rest", r.Apply("token=ab12cd34 rest"))
}

// The gitleaks loader resolves an unset secretGroup: the only group
// when there is one, the whole match when there are several (the
// first-group rule leaked fragments).
func TestLoader_SecretGroupResolution(t *testing.T) {
	p, err := redact.LoadGitleaksPolicy(redact.DefaultGitleaksPath())
	require.NoError(t, err)
	byID := map[string]redact.RuleSpec{}
	for _, s := range p.Rules {
		byID[s.ID] = s
	}
	assert.Equal(t, 1, byID["adafruit-api-key"].SecretGroup)
	assert.Equal(t, 0, byID["jwt-base64"].SecretGroup)
	assert.Equal(t, 0, byID["microsoft-teams-webhook"].SecretGroup)
	assert.Equal(t, 0, byID["github-pat"].SecretGroup, "no groups: whole match")
	assert.Equal(t, []string{"adafruit"}, byID["adafruit-api-key"].Keywords)
}

func TestEntropy_FloorIsInclusive(t *testing.T) {
	r := specRedactor(t, redact.Tag, redact.RuleSpec{ID: "e", Regex: `[a-h]{8}`, Entropy: 3})
	// eight distinct runes: exactly 3 bits, at the floor, dropped.
	assert.Equal(t, "abcdefgh", r.Apply("abcdefgh"))
	r = specRedactor(t, redact.Tag, redact.RuleSpec{ID: "e", Regex: `[a-h]{8}`, Entropy: 2.9})
	assert.Equal(t, "<e>", r.Apply("abcdefgh"))
	assert.Equal(t, "aaaaaaaa", specRedactor(t, redact.Tag,
		redact.RuleSpec{ID: "e", Regex: `[a-h]{8}`, Entropy: 0.5}).Apply("aaaaaaaa"))
}

// Each case gives Apply's output (fail closed: whole-target regexes,
// no stopwords) and whether Scan (gitleaks detection) exempts it.
func TestAllowlist_Targets(t *testing.T) {
	rule := func(a redact.AllowlistSpec) redact.RuleSpec {
		return redact.RuleSpec{ID: "k", Regex: `key=([A-Za-z0-9]{6})`, SecretGroup: 1,
			Allowlists: []redact.AllowlistSpec{a}}
	}
	cases := []struct {
		name         string
		a            redact.AllowlistSpec
		in           string
		apply        string
		detectExempt bool
	}{
		{"secret regex whole", redact.AllowlistSpec{Regexes: []string{`^EXAMPL`}}, "key=EXAMPL", "key=EXAMPL", true},
		{"secret regex miss", redact.AllowlistSpec{Regexes: []string{`^EXAMPL`}}, "key=Q1w2E3", "key=<k>", false},
		{"match target whole", redact.AllowlistSpec{RegexTarget: "match", Regexes: []string{`key=Q.*`}}, "key=Q1w2E3", "key=Q1w2E3", true},
		{"match target partial", redact.AllowlistSpec{RegexTarget: "match", Regexes: []string{`^key=`}}, "key=Q1w2E3", "key=<k>", true},
		{"line target whole", redact.AllowlistSpec{RegexTarget: "line", Regexes: []string{`.*# fixture`}}, "a\nkey=Q1w2E3 # fixture\nb", "a\nkey=Q1w2E3 # fixture\nb", true},
		{"line target partial", redact.AllowlistSpec{RegexTarget: "line", Regexes: []string{`# fixture`}}, "a\nkey=Q1w2E3 # fixture\nb", "a\nkey=<k> # fixture\nb", true},
		{"line target other line", redact.AllowlistSpec{RegexTarget: "line", Regexes: []string{`# fixture`}}, "# fixture\nkey=Q1w2E3", "# fixture\nkey=<k>", false},
		{"stopword", redact.AllowlistSpec{StopWords: []string{"DUMMY"}}, "key=dummy1", "key=<k>", true},
		{"and needs both", redact.AllowlistSpec{Condition: "AND", Regexes: []string{`^d`}, StopWords: []string{"zz"}}, "key=dummy1", "key=<k>", false},
		{"and with path never", redact.AllowlistSpec{Condition: "AND", Regexes: []string{`.+`}, Paths: []string{`.`}}, "key=dummy1", "key=<k>", false},
		{"empty match is no hit", redact.AllowlistSpec{Regexes: []string{`x*`}}, "key=Q1w2E3", "key=<k>", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := specRedactor(t, redact.Tag, rule(c.a))
			assert.Equal(t, c.apply, r.Apply(c.in))
			assert.Equal(t, c.detectExempt, len(r.Scan(c.in)) == 0, "Scan exemption")
		})
	}
}

func TestAllowlist_GlobalAppliesToEveryRule(t *testing.T) {
	r, err := redact.NewFromPolicy(redact.Policy{
		Allowlists: []redact.AllowlistSpec{{Regexes: []string{`[a-z]-example`}}},
		Rules:      []redact.RuleSpec{{ID: "a", Regex: `a-[a-z]{7}`}, {ID: "b", Regex: `b-[a-z]{7}`}},
	})
	require.NoError(t, err)
	assert.Equal(t, "a-example b-example ***REDACTED***", r.Apply("a-example b-example a-zqwerty"))
	assert.Equal(t, uint64(2), r.Stats().Allowed)
}

func TestKeywords_GateTheRule(t *testing.T) {
	r := specRedactor(t, redact.Tag, redact.RuleSpec{ID: "k", Regex: `[0-9a-f]{12}`, Keywords: []string{"KEY"}})
	assert.Equal(t, "0123456789ab", r.Apply("0123456789ab"))
	assert.Equal(t, "Key: <k>", r.Apply("Key: 0123456789ab"))
	// Lower-casing is Unicode, as gitleaks': the Kelvin sign is a k.
	assert.Equal(t, "Key: <k>", r.Apply("Key: 0123456789ab"))
}

func TestSpans_MultibyteBoundaries(t *testing.T) {
	r := specRedactor(t, redact.Tag, redact.RuleSpec{ID: "u", Regex: `é[a-z]{4}ü`})
	in := "日本 éabcdü 語"
	assert.Equal(t, []redact.Span{{7, 15, "u"}}, r.Spans(in))
	assert.Equal(t, "日本 <u> 語", r.Apply(in))
}

func TestPatches_GlobalAllowlistAnchored(t *testing.T) {
	r, err := redact.NewEmbedded()
	require.NoError(t, err)
	// A secret merely containing "false" was exempt upstream.
	tok := "ghp_" + "falseQ8xZ2LmN4pR7tV1wY3kB6dF9hJ0sA5c"
	assert.NotContains(t, r.Apply("token "+tok), tok)
	p := r.Policy()
	assert.Contains(t, p.Allowlists[0].Regexes, `(?i)^(?:true|false|null)$`)
}

func TestPatches_GenericKeyNoLongerExemptsAuthorAndPrimary(t *testing.T) {
	r, err := redact.NewEmbedded()
	require.NoError(t, err)
	for _, in := range []string{
		"primary_key = " + "Zx9Qm2Lp7Wv4Rt8K",
		"author_token = " + "Zx9Qm2Lp7Wv4Rt8K",
	} {
		assert.NotContains(t, r.Apply(in), "Zx9Qm2Lp7Wv4Rt8K", in)
	}
}

func TestPolicy_RoundTrip(t *testing.T) {
	p, err := redact.EmbeddedPolicy()
	require.NoError(t, err)
	b, err := json.Marshal(p)
	require.NoError(t, err)
	var back redact.Policy
	require.NoError(t, json.Unmarshal(b, &back))
	r, err := redact.NewFromPolicy(back)
	require.NoError(t, err)
	assert.Equal(t, p, r.Policy())
	assert.Greater(t, len(r.Rules()), 200)
	rule := r.Rules()[0]
	assert.Equal(t, rule.Pattern(), rule.Regexp().String())
}

func TestPolicy_StrictCompile(t *testing.T) {
	_, err := redact.NewFromPolicy(redact.Policy{Rules: []redact.RuleSpec{{ID: "x", Regex: `(a)`, SecretGroup: 2}}})
	assert.Error(t, err)
	_, err = redact.NewFromPolicy(redact.Policy{Rules: []redact.RuleSpec{{ID: "x", Regex: `a(?=b)`}}})
	assert.Error(t, err)
	_, err = redact.NewFromPolicy(redact.Policy{Version: "v2"})
	assert.Error(t, err)
}

func TestNewEmbedded_IgnoresEnvOverrides(t *testing.T) {
	t.Setenv("KIT_REDACT_RULES_PATH", "/nonexistent/rules.toml")
	t.Setenv("KIT_REDACT_PII_RULES_PATH", "/nonexistent/pii.toml")
	r, err := redact.NewEmbedded()
	require.NoError(t, err)
	assert.Greater(t, len(r.Rules()), 200)
	assert.True(t, strings.Contains(r.Apply("mail dev@example.com"), "***REDACTED***"))
}
