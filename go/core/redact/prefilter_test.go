package redact

import (
	"fmt"
	"math/rand"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// The prefilter may only skip a rule that cannot match. These tests
// hold it to that against the rules alone (noPrefilter) over the full
// default corpus, with inputs generated from each rule's own pattern.

// corpus loads the default rule corpus into a fresh Redactor.
func corpus(t testing.TB, prefiltered bool) *Redactor {
	t.Helper()
	r := New()
	r.noPrefilter = !prefiltered
	gl, err := LoadGitleaks(DefaultGitleaksPath())
	if err != nil {
		t.Fatal(err)
	}
	pii, err := LoadPresidio(DefaultPresidioPath())
	if err != nil {
		t.Fatal(err)
	}
	return r.AddRules(gl...).AddRules(pii...)
}

// genMatch appends to sb a string built by walking re: a random
// branch of each alternation, a random count within each repeat, a
// random member of each class and, under (?i), a random member of
// each literal rune's fold orbit (non-ASCII partners included).
func genMatch(re *syntax.Regexp, rnd *rand.Rand, sb *strings.Builder) {
	switch re.Op {
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			if re.Flags&syntax.FoldCase != 0 {
				orbit := []rune{r}
				for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
					orbit = append(orbit, f)
				}
				r = orbit[rnd.Intn(len(orbit))]
			}
			sb.WriteRune(r)
		}
	case syntax.OpCharClass:
		if len(re.Rune) == 0 {
			return
		}
		i := rnd.Intn(len(re.Rune)/2) * 2
		lo, hi := re.Rune[i], re.Rune[i+1]
		span := int(hi-lo) + 1
		if span > 1<<16 {
			span = 1 << 16
		}
		sb.WriteRune(lo + rune(rnd.Intn(span)))
	case syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		pool := []rune("aZ9_-. =:\u00e9\u212a\u017f")
		sb.WriteRune(pool[rnd.Intn(len(pool))])
	case syntax.OpCapture:
		genMatch(re.Sub[0], rnd, sb)
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		lo, hi := 0, 3
		switch re.Op {
		case syntax.OpPlus:
			lo = 1
		case syntax.OpQuest:
			hi = 1
		case syntax.OpRepeat:
			lo, hi = re.Min, re.Max
			if hi < 0 || hi > lo+3 {
				hi = lo + 3
			}
		}
		for n := lo + rnd.Intn(hi-lo+1); n > 0; n-- {
			genMatch(re.Sub[0], rnd, sb)
		}
	case syntax.OpConcat:
		for _, s := range re.Sub {
			genMatch(s, rnd, sb)
		}
	case syntax.OpAlternate:
		genMatch(re.Sub[rnd.Intn(len(re.Sub))], rnd, sb)
	}
}

// samples returns inputs derived from pattern: generated matches,
// wrapped in context, and with ASCII case flipped.
func samples(pattern string, rnd *rand.Rand, n int) []string {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil
	}
	re = re.Simplify()
	ctx := []string{"", " ", "token=", "Bearer ", "\n", "x", "\u00e9 ", "\xff", "key: \"", "'"}
	var out []string
	for range n {
		var sb strings.Builder
		genMatch(re, rnd, &sb)
		s := sb.String()
		wrapped := ctx[rnd.Intn(len(ctx))] + s + ctx[rnd.Intn(len(ctx))]
		flipped := []byte(wrapped)
		for i, b := range flipped {
			if rnd.Intn(3) == 0 && ('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z') {
				flipped[i] = b ^ 0x20
			}
		}
		out = append(out, s, wrapped, string(flipped))
	}
	return out
}

// assertSound fails for every rule of r that matches s while the
// prefilter excludes it.
func assertSound(t *testing.T, r *Redactor, s string, only ...int) bool {
	t.Helper()
	cand := candidateRules(r, s, nil)
	check := func(i int) bool {
		if r.rules[i].re.MatchString(s) && !has(cand, i) {
			t.Errorf("rule %s matches %q but the prefilter skipped it (screen %+v)",
				r.rules[i].id, s, screenFor(r.rules[i].re.String()))
			return false
		}
		return true
	}
	ok := true
	if len(only) > 0 {
		for _, i := range only {
			ok = check(i) && ok
		}
		return ok
	}
	for i := range r.rules {
		ok = check(i) && ok
	}
	return ok
}

func TestPrefilter_NeverSkipsAMatchingRule(t *testing.T) {
	r := corpus(t, true)
	rnd := rand.New(rand.NewSource(1502))
	matchedOwn := 0
	for i, rule := range r.rules {
		hit := false
		for k, s := range samples(rule.re.String(), rnd, 20) {
			if rule.re.MatchString(s) {
				hit = true
			}
			if k%15 == 0 {
				assertSound(t, r, s) // every rule, on a subset
			} else {
				assertSound(t, r, s, i)
			}
		}
		if hit {
			matchedOwn++
		}
	}
	// The generator must actually produce matches, or the test proves
	// nothing.
	if matchedOwn < len(r.rules)*9/10 {
		t.Fatalf("only %d of %d rules matched their generated samples", matchedOwn, len(r.rules))
	}
}

// Deterministic half of the performance gate: the literal screen only
// pays off for rules it can screen. A rule with no derivable literal
// runs on every input long enough for it, so a corpus update that adds
// such rules, or an analysis change that loses them, shows up here
// before it shows up as time. sourcegraph-access-token is the known
// one: a bare 40-hex-digit alternative has no literal.
func TestPrefilter_DefaultCorpusIsScreened(t *testing.T) {
	const maxUnscreened = 2
	var unscreened []string
	r := corpus(t, true)
	for _, rule := range r.rules {
		if len(screenFor(rule.re.String()).clauses) == 0 {
			unscreened = append(unscreened, rule.id)
		}
	}
	if len(unscreened) > maxUnscreened {
		t.Errorf("%d of %d default rules have no literal screen (budget %d): %v",
			len(unscreened), len(r.rules), maxUnscreened, unscreened)
	}
}

// Shapes where a careless screen would skip a real match: non-ASCII
// fold partners, invalid UTF-8 matched as U+FFFD, empty branches,
// optional pieces and minimum-length bounds.
func TestPrefilter_EdgeCases(t *testing.T) {
	cases := []struct{ pattern, input string }{
		{`(?i)key`, "\u212aey"},                    // Kelvin sign folds to k
		{`(?i)secret`, "\u017fecret"},              // long s folds to s
		{`(?i)\x{01C5}z`, "\u01c6z"},               // title-case orbit of three
		{`(?i)caf\x{e9}`, "CAF\u00c9"},             // non-ASCII fold
		{`\x{e9}t\x{e9}`, "\u00e9t\u00e9"},         // non-ASCII, case-sensitive
		{`[kK]ey`, "Key"},                          // class, not fold
		{`\x{FFFD}abc`, "\xffabc"},                 // U+FFFD matches an invalid byte
		{`[\x{FFFD}x]yz`, "\xfeyz"},                // class holding U+FFFD
		{`a.c`, "a\xffc"},                          // any-char over an invalid byte
		{`[\x{80}-\x{10FFFF}]{3}`, "\xff\xfe\xfd"}, // 3 bytes, not 6
		{`(?:tok|)en_[0-9]{2}`, "en_42"},           // empty branch
		{`(?:ab)?cd`, "cd"},
		{`x(?:yz)*w`, "xw"},
		{`\bpass\b`, "a pass b"},
		{`^token$`, "token"},
		{`(?-i:[Aa]pi)_(?i:KEY)`, "Api_key"},
		{`(?:a|b|c|d|e|f|g|h|i|j|k|l|m|n|o|p|q|r|s|t|u|v|w|x|y|z){3}9`, "qrs9"},
	}
	for _, c := range cases {
		t.Run(c.pattern, func(t *testing.T) {
			if !regexp.MustCompile(c.pattern).MatchString(c.input) {
				t.Fatalf("fixture does not match: %q", c.input)
			}
			r := New()
			if _, err := r.AddRule("edge", c.pattern, ""); err != nil {
				t.Fatal(err)
			}
			assertSound(t, r, c.input)
			if got := r.Apply(c.input); got == c.input {
				t.Errorf("Apply left %q unredacted", c.input)
			}
		})
	}
}

// The screen has to earn its keep: a rule whose literal is absent, or
// whose shortest match is longer than the input, is skipped.
func TestPrefilter_SkipsRulesThatCannotMatch(t *testing.T) {
	r := New()
	for id, p := range map[string]string{
		"lit":  `(?i)adafruit[a-z0-9]{8}`,
		"both": `(?i)(?:token|secret)\s*[=:]\s*[a-z]{4}`,
		"long": `[a-f0-9]{40}`,
	} {
		if _, err := r.AddRule(id, p, ""); err != nil {
			t.Fatal(err)
		}
	}
	idx := map[string]int{}
	for i, rule := range r.rules {
		idx[rule.id] = i
	}
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"nothing to see here", nil},
		{"the token was rotated", nil}, // keyword without an assignment
		{"secret = abcd", []string{"both"}},
		{"ADAFRUIT12345678", []string{"lit"}},
		{"0123456789abcdef", nil}, // hex, but shorter than 40
		{strings.Repeat("ab", 20), []string{"long"}},
	} {
		cand := candidateRules(r, c.in, nil)
		var got []string
		for id, i := range idx {
			if has(cand, i) {
				got = append(got, id)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: candidates %v, want %v", c.in, got, c.want)
		}
	}
}

// A replacement can create text a later rule matches. The prefilter
// screens the rewritten input again, so the chain behaves as it does
// with every rule running.
func TestPrefilter_ReplacementFeedsLaterRule(t *testing.T) {
	build := func(prefiltered bool) *Redactor {
		r := New()
		r.noPrefilter = !prefiltered
		if _, err := r.AddRule("first", `pin-[0-9]{4}`, "zz_tok_ABCDEFGH"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.AddRule("second", `tok_[A-Z]{8}`, "<second>"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.SetReplacement(Tag); err != nil {
			t.Fatal(err)
		}
		return r
	}
	const in = "door pin-1234 opened"
	want := build(false).Apply(in)
	if want != "door zz_<second> opened" {
		t.Fatalf("reference = %q; fixture no longer chains", want)
	}
	if got := build(true).Apply(in); got != want {
		t.Errorf("prefiltered Apply = %q, want %q", got, want)
	}
	if got := string(build(true).ApplyBytes([]byte(in))); got != want {
		t.Errorf("prefiltered ApplyBytes = %q, want %q", got, want)
	}
}

// Apply, ApplyBytes, Scan, observers and Stats agree with the rules
// alone on text mixing many rules' matches.
func TestPrefilter_EquivalentToRulesAlone(t *testing.T) {
	ref, pf := corpus(t, false), corpus(t, true)
	var refSeen, pfSeen []Match
	for _, x := range []struct {
		r    *Redactor
		seen *[]Match
	}{{ref, &refSeen}, {pf, &pfSeen}} {
		if _, err := x.r.SetReplacement(Tag); err != nil {
			t.Fatal(err)
		}
		seen := x.seen
		x.r.OnMatch(func(m Match) { *seen = append(*seen, m) })
	}

	rnd := rand.New(rand.NewSource(7))
	var inputs []string
	for _, rule := range ref.rules {
		inputs = append(inputs, samples(rule.re.String(), rnd, 1)...)
	}
	// Documents mixing several rules' matches, plus the benchmark
	// fixtures.
	for range 20 {
		var sb strings.Builder
		for range 6 {
			sb.WriteString(inputs[rnd.Intn(len(inputs))])
			sb.WriteString(" log line user_id=42 ")
		}
		inputs = append(inputs, sb.String())
	}
	inputs = append(inputs, makePayloadInternal(4096, 5), "")

	for k, in := range inputs {
		refSeen, pfSeen = refSeen[:0], pfSeen[:0]
		want := ref.Apply(in)
		if got := pf.Apply(in); got != want {
			t.Fatalf("Apply(%q)\n got %q\nwant %q", in, got, want)
		}
		if !slices.Equal(refSeen, pfSeen) {
			t.Fatalf("observers differ on %q:\n got %v\nwant %v", in, pfSeen, refSeen)
		}
		if k%4 != 0 {
			continue // the byte and scan paths share the screen; sample them
		}
		if got := string(pf.ApplyBytes([]byte(in))); got != string(ref.ApplyBytes([]byte(in))) {
			t.Fatalf("ApplyBytes(%q) = %q", in, got)
		}
		if got, want := pf.Scan(in), ref.Scan(in); !slices.Equal(got, want) {
			t.Fatalf("Scan(%q)\n got %v\nwant %v", in, got, want)
		}
	}
	if rs, ps := ref.Stats(), pf.Stats(); rs.Matches != ps.Matches || fmt.Sprint(rs.ByRule) != fmt.Sprint(ps.ByRule) {
		t.Errorf("stats differ: ref %d %v, prefiltered %d %v", rs.Matches, rs.ByRule, ps.Matches, ps.ByRule)
	}
}

// makePayloadInternal mirrors the benchmark fixture (redact_test
// package) for this internal test.
func makePayloadInternal(size, secrets int) string {
	const phrase = "GET /api/v1/users 200 12ms user_id=42 trace=a1b2c3 "
	s := strings.Repeat(phrase, size/len(phrase)+1)[:size]
	fixtures := []string{
		"sk-" + strings.Repeat("a", 20) + "T3BlbkFJ" + strings.Repeat("b", 20),
		"AKIAIOSFODNN7CLIENTX",
		"jad@example.com",
		"192.168.1.42",
		"ghp_" + "1234567890abcdefghijABCDEFGHIJ123456",
	}
	step := size / (secrets + 1)
	var sb strings.Builder
	for i := range secrets {
		sb.WriteString(s[i*step : (i+1)*step])
		sb.WriteString(" " + fixtures[i%len(fixtures)] + " ")
	}
	sb.WriteString(s[secrets*step:])
	return sb.String()
}

// FuzzPrefilterSound: for any input, a default rule that matches is a
// candidate. `go test -fuzz=FuzzPrefilterSound ./go/core/redact/`
// explores beyond the seeds.
func FuzzPrefilterSound(f *testing.F) {
	for _, s := range []string{
		"token=abcdef0123456789abcd", "\u212aey: \u017fecret-value-1234",
		"\xff\xfe AKIA\xffIOSFODNN7EXAMPLE", "sgp_" + strings.Repeat("a1", 20),
		"4111 1111 1111 1111", "jad@example.com 192.168.1.42 +14155550123",
		"xoxb-" + "123456789012-1234567890123-abcdefghijABCDEFGHIJabcd",
	} {
		f.Add(s)
	}
	r := corpus(f, true)
	f.Fuzz(func(t *testing.T, s string) {
		assertSound(t, r, s)
	})
}
