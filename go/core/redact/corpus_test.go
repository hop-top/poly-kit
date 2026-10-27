package redact

import (
	"math/rand"
	"regexp/syntax"
	"strings"
	"testing"

	scoperules "hop.top/kit/go/core/scope/rules"
)

func embedded(t testing.TB) *Redactor {
	t.Helper()
	r, err := NewEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Every rule's findings on generated keys (seeded, from the rule's own
// pattern) leave no fragment behind: the secret of each finding is
// gone from the output of the full default policy.
func TestCorpus_GeneratedKeysLeaveNoFragments(t *testing.T) {
	r := embedded(t)
	rnd := rand.New(rand.NewSource(1667))
	found := 0
	for _, rule := range r.rules {
		re, err := syntax.Parse(rule.spec.Regex, syntax.Perl)
		if err != nil {
			t.Fatal(err)
		}
		re = re.Simplify()
		hitRule := false
		for range 12 {
			var sb strings.Builder
			genMatch(re, rnd, &sb)
			in := "log: " + strings.Join(rule.spec.Keywords, " ") + " " + sb.String() + " end"
			out := r.Apply(in)
			for _, m := range r.Scan(in) {
				if m.RuleID == rule.spec.ID {
					hitRule = true
				}
				if len(m.Original) >= 6 && strings.Contains(out, m.Original) {
					t.Errorf("rule %s: finding %q of %s survives in %q", rule.spec.ID, m.Original, m.RuleID, out)
				}
			}
		}
		if hitRule {
			found++
		}
	}
	if found < len(r.rules)*8/10 {
		t.Fatalf("only %d of %d rules found their generated keys", found, len(r.rules))
	}
}

// A rule with a secret group reports exactly that group's span, not
// the whole match.
func TestCorpus_SecretGroupSpan(t *testing.T) {
	r := embedded(t)
	rnd := rand.New(rand.NewSource(1829))
	checked := 0
	for _, rule := range r.rules {
		g := rule.spec.SecretGroup
		if g == 0 {
			continue
		}
		single := New().AddRules(rule)
		re, _ := syntax.Parse(rule.spec.Regex, syntax.Perl)
		re = re.Simplify()
		for range 6 {
			var sb strings.Builder
			genMatch(re, rnd, &sb)
			s := strings.Join(rule.spec.Keywords, " ") + " " + sb.String()
			sub := rule.re.FindStringSubmatchIndex(s)
			found := single.Scan(s)
			if sub == nil || len(found) == 0 || sub[2*g] < 0 || sub[2*g+1] == sub[2*g] {
				continue
			}
			if strings.ContainsRune(s[sub[0]:sub[1]], '\n') {
				continue // trimmed matches clip the group; covered by vectors
			}
			if found[0].Start != sub[2*g] || found[0].End != sub[2*g+1] {
				t.Errorf("rule %s: span [%d,%d), group %d is [%d,%d) in %q",
					rule.spec.ID, found[0].Start, found[0].End, g, sub[2*g], sub[2*g+1], s)
			}
			checked++
		}
	}
	if checked < 100 {
		t.Fatalf("only %d secret-group findings checked", checked)
	}
}

func TestGitleaksPatchesApply(t *testing.T) {
	p, err := parseGitleaks(scoperules.GitleaksContent)
	if err != nil {
		t.Fatal(err)
	}
	if n := applyPatches(p.Allowlists, p.Rules, gitleaksPatches); n != len(gitleaksPatches) {
		t.Fatalf("%d of %d corpus patches applied to the vendored corpus", n, len(gitleaksPatches))
	}
	if n := applyPatches(p.Allowlists, p.Rules, gitleaksPatches); n != 0 {
		t.Fatalf("%d patches still find their text once applied", n)
	}
}

// The keyword scanner agrees exactly with the reference gate.
func TestKeywordScanner_MatchesReference(t *testing.T) {
	r := embedded(t)
	rnd := rand.New(rand.NewSource(42))
	inputs := []string{"", "KEY", "Key", "İnfo", "twitter TWITTER", "\xff\xfe slack"}
	for _, rule := range r.rules[:60] {
		inputs = append(inputs, samples(rule.spec.Regex, rnd, 1)...)
	}
	for _, in := range inputs {
		lower, _ := keywordText(in)
		kw := make([]uint64, r.pf.words())
		candidates(r.kw, lower.(string), kw)
		full := r.candidates(in, nil)
		lit := make([]uint64, r.pf.words())
		candidates(r.pf, in, lit)
		for i := range r.rules {
			if has(full, i) != (has(lit, i) && has(kw, i)) {
				t.Errorf("%q: rule %s combined screen disagrees with literal AND keyword", in, r.rules[i].spec.ID)
			}
			if want := keywordGate(r.rules[i].spec.Keywords, lower); has(kw, i) != want {
				t.Errorf("%q: rule %s keyword scanner %v, reference %v", in, r.rules[i].spec.ID, has(kw, i), want)
			}
		}
	}
}
