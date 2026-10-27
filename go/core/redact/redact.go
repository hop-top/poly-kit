// Package redact strips secrets, tokens, and PII from text before it leaves
// the process — log lines, telemetry payloads, LLM prompts, error reports,
// any user-facing output that may have absorbed sensitive data.
//
// The model:
//
//   - A Redactor holds an ordered list of compiled Rules, global
//     allowlists and a single Replacement strategy (Mask / Tag / Hash /
//     Custom).
//   - Apply finds every rule's secret spans on the original input,
//     merges overlapping and adjacent spans, and replaces them in one
//     pass (see spans.go). A replacement is never scanned again, so no
//     rule sees another rule's placeholder.
//   - Rules follow gitleaks semantics: a keyword gate, a secret group,
//     an entropy floor and regex/stopword allowlists.
//   - A literal prefilter skips the rules that cannot match the input
//     before any regex runs.
//   - The engine is stdlib regexp (RE2). Linear-time matching is the entire
//     reason this can run on adversarial input — LLM outputs, scraped pages,
//     attacker-supplied prompts.
//
// Default() lazily loads the vendored gitleaks corpus + Presidio PII pack
// at first use.
package redact

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	charmlog "charm.land/log/v2"
)

// Replacement selects how a matched substring is rewritten.
type Replacement int

const (
	// Mask replaces every match with a fixed sentinel ("***REDACTED***").
	// Default; safe but loses the kind-of-secret signal.
	Mask Replacement = iota
	// Tag wraps the rule id ("<openai-api-key>"). Recommended for
	// diagnosable logs — preserves which kind of secret was present
	// without leaking the value.
	Tag
	// Hash emits a stable sha256 prefix ("sha256:6ca13d52"). Useful for
	// log correlation across runs without exposing the secret.
	Hash
	// Custom delegates to a user-supplied formatter. Required argument
	// to SetReplacement when used.
	Custom
)

// Match is emitted to observers for every redaction and returned by
// Scan. Original is the text replaced (or, for Scan and OnAllowed, the
// secret found); Start and End are its byte offsets in the input.
type Match struct {
	RuleID      string
	Original    string
	Replacement string
	Start, End  int
}

// Span is one region Apply replaces, in byte offsets of the input.
// RuleID names the rule whose replacement is written.
type Span struct {
	Start, End int
	RuleID     string
}

// Stats is a snapshot of Redactor activity.
type Stats struct {
	Rules int
	// Matches counts replaced regions; merged spans count once.
	Matches uint64
	// Allowed counts matches that an allowlist exempted from redaction.
	// A non-zero value means secrets left the process unredacted by
	// policy — alert on unexpected growth.
	Allowed     uint64
	ByRule      map[string]uint64
	LastMatchAt time.Time
}

// Redactor applies a set of Rules to text, replacing matched substrings
// per its configured strategy. Safe for concurrent Apply / Scan / Stats
// once construction is complete; AddRule, AddAllowlists and
// SetReplacement are not concurrency-safe with Apply running.
type Redactor struct {
	rules    []Rule
	strategy Replacement
	custom   func(Match) string

	exactAllowlist []string
	allowlists     []allowlist
	allowlistSpecs []AllowlistSpec
	// needLine is set once any allowlist targets the line.
	needLine bool

	// pf indexes the rules' required literals, kw their keyword gates
	// and both both, for ASCII input (see prefilter.go); bothRedact
	// leaves out the gates of distinctive rules, which redaction skips.
	// distinct is the bitset of distinctive rules. Maintained by
	// AddRules.
	pf, kw, both, bothRedact *prefilter
	distinct                 []uint64
	// noPrefilter runs every rule on every input; the reference
	// behavior the prefilter is tested against. Keyword gates still
	// apply: they are rule semantics, not an optimization.
	noPrefilter bool

	obsMu      sync.RWMutex
	observers  []func(Match)
	allowedObs []func(Match)

	matches      atomic.Uint64
	allowedCount atomic.Uint64
	statsMu      sync.Mutex
	byRule       map[string]uint64
	lastAt       time.Time

	// logger handles internal warnings (e.g. custom-formatter panic
	// recovery). Defaults to kit/log wired to the active viper; override
	// via WithLogger.
	logger *charmlog.Logger
}

// New returns an empty Redactor with the Mask strategy. Options apply
// in order before any rules are added; pass WithLogger to override the
// internal warning logger.
func New(opts ...Option) *Redactor {
	r := &Redactor{
		strategy: Mask,
		byRule:   make(map[string]uint64),
		logger:   defaultLogger(),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// AddRule compiles pattern and appends a Rule with the given id and
// optional rule-local replacement template. The rule redacts the whole
// match. Returns the receiver for chaining; on regex compile error
// returns nil and the error.
func (r *Redactor) AddRule(id, pattern, replacement string) (*Redactor, error) {
	rule, err := NewRule(id, pattern, replacement)
	if err != nil {
		return nil, err
	}
	r.AddRules(rule)
	return r, nil
}

// AddRules appends pre-compiled rules in bulk. Used by the loader.
func (r *Redactor) AddRules(rules ...Rule) *Redactor {
	if r.pf == nil {
		r.pf, r.kw, r.both, r.bothRedact = &prefilter{}, &prefilter{}, &prefilter{}, &prefilter{}
	}
	for _, rule := range rules {
		pattern := "" // a zero Rule screens as always-run
		if rule.re != nil {
			pattern = rule.re.String()
		}
		sc := screenFor(pattern)
		r.pf.addScreen(sc)
		r.kw.addScreen(keywordScreen(rule.spec.Keywords))
		r.both.addScreen(sc.withKeywords(rule.spec.Keywords))
		if rule.spec.Distinctive {
			r.bothRedact.addScreen(sc)
		} else {
			r.bothRedact.addScreen(sc.withKeywords(rule.spec.Keywords))
		}
		i := len(r.rules)
		for len(r.distinct) <= i/64 {
			r.distinct = append(r.distinct, 0)
		}
		if rule.spec.Distinctive {
			r.distinct[i/64] |= 1 << (i % 64)
		}
		for i := range rule.allowlists {
			r.needLine = r.needLine || rule.allowlists[i].target == "line"
		}
		r.rules = append(r.rules, rule)
	}
	return r
}

// AddAllowlists compiles gitleaks-style allowlists that apply to every
// rule, in addition to each rule's own. Returns nil and the error when
// one does not compile; none is added then.
func (r *Redactor) AddAllowlists(specs ...AllowlistSpec) (*Redactor, error) {
	als, err := compileAllowlists(specs)
	if err != nil {
		return nil, err
	}
	for i := range als {
		r.needLine = r.needLine || als[i].target == "line"
		r.allowlistSpecs = append(r.allowlistSpecs, specs[i].clone())
	}
	r.allowlists = append(r.allowlists, als...)
	return r, nil
}

// Rules returns the rules in evaluation order. Rule values are
// read-only; the slice is a copy.
func (r *Redactor) Rules() []Rule {
	out := make([]Rule, len(r.rules))
	copy(out, r.rules)
	return out
}

// SetReplacement chooses the replacement strategy. fn is required when
// strategy is Custom; ignored otherwise. Returns an error if Custom is
// requested without a fn.
func (r *Redactor) SetReplacement(strategy Replacement, fn ...func(Match) string) (*Redactor, error) {
	if strategy == Custom {
		if len(fn) == 0 || fn[0] == nil {
			return nil, errors.New("redact: Custom strategy requires a non-nil formatter")
		}
		r.custom = fn[0]
	} else {
		r.custom = nil
	}
	r.strategy = strategy
	return r, nil
}

// AllowExact adds literals to the global allowlist. A finding is
// exempted only when its secret or its whole match equals an entry in
// full, so an allowlisted fixture cannot be extended into a live
// credential that inherits the exemption.
//
// Prefer this over Allow. Entries are compared literally — never as
// regex, keeping ReDoS risk confined to the rules side — and empty
// entries are ignored.
//
// Exempted matches are reported to OnAllowed observers and counted in
// Stats().Allowed, so a pass-through is auditable rather than silent.
func (r *Redactor) AllowExact(vals ...string) *Redactor {
	r.exactAllowlist = append(r.exactAllowlist, vals...)
	return r
}

// OnMatch registers an observer fired for every replaced region (after
// allowlist filtering and span merging, before substitution). Multiple
// calls chain in registration order; nil clears the chain.
func (r *Redactor) OnMatch(fn func(Match)) *Redactor {
	r.obsMu.Lock()
	if fn == nil {
		r.observers = nil
	} else {
		r.observers = append(r.observers, fn)
	}
	r.obsMu.Unlock()
	return r
}

// OnAllowed registers an observer fired for every match an allowlist
// exempted from redaction. Passing nil clears registered observers.
//
// Allowlisted values leave the process unredacted; this hook is how that
// stays visible to audit tooling. Callbacks receive the matched text —
// treat it as toxic (count it, fingerprint it; do not log it verbatim).
func (r *Redactor) OnAllowed(fn func(Match)) *Redactor {
	r.obsMu.Lock()
	if fn == nil {
		r.allowedObs = nil
	} else {
		r.allowedObs = append(r.allowedObs, fn)
	}
	r.obsMu.Unlock()
	return r
}

// Apply returns s with every secret span replaced per the active
// strategy, in a single pass over the original text.
func (r *Redactor) Apply(s string) string {
	if len(r.rules) == 0 || s == "" {
		return s
	}
	regions := regionsOf(r, s, true)
	if len(regions) == 0 {
		return s
	}
	return string(render(r, make([]byte, 0, len(s)), s, regions))
}

// ApplyBytes is the []byte counterpart to Apply. It returns b itself
// when nothing is redacted.
func (r *Redactor) ApplyBytes(b []byte) []byte {
	if len(r.rules) == 0 || len(b) == 0 {
		return b
	}
	regions := regionsOf(r, b, true)
	if len(regions) == 0 {
		return b
	}
	return render(r, make([]byte, 0, len(b)), b, regions)
}

// render appends s with regions replaced to out, firing observers and
// recording stats per region.
func render[T string | []byte](r *Redactor, out []byte, s T, regions []region) []byte {
	last := 0
	for _, g := range regions {
		rule := &r.rules[g.rule]
		m := Match{RuleID: rule.spec.ID, Original: string(s[g.start:g.end]), Start: g.start, End: g.end}
		m.Replacement = r.format(rule, m)
		r.fireObservers(m)
		r.recordMatch(rule.spec.ID)
		out = append(out, s[last:g.start]...)
		out = append(out, m.Replacement...)
		last = g.end
	}
	return append(out, s[last:]...)
}

// Scan returns every finding of every rule, before span merging,
// without replacing or recording stats. Findings come in rule order,
// then by position; Original is the secret span.
//
// Scan is detection: allowlists follow gitleaks (substring regexes,
// stopwords), so it reports what gitleaks would. Apply fails closed and
// can redact findings Scan leaves out; use Spans for exactly what Apply
// replaces.
func (r *Redactor) Scan(s string) []Match {
	if len(r.rules) == 0 || s == "" {
		return nil
	}
	hits := collect(r, s, detect)
	out := make([]Match, 0, len(hits))
	for _, h := range hits {
		out = append(out, Match{
			RuleID:   r.rules[h.rule].spec.ID,
			Original: s[h.start:h.end],
			Start:    h.start,
			End:      h.end,
		})
	}
	return out
}

// Spans returns the merged regions Apply would replace in s, in input
// order, without replacing or recording stats.
func (r *Redactor) Spans(s string) []Span {
	if len(r.rules) == 0 || s == "" {
		return nil
	}
	regions := regionsOf(r, s, false)
	out := make([]Span, len(regions))
	for i, g := range regions {
		out[i] = Span{Start: g.start, End: g.end, RuleID: r.rules[g.rule].spec.ID}
	}
	return out
}

// Stats returns a snapshot. ByRule is deep-copied; safe to mutate.
func (r *Redactor) Stats() Stats {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	cp := make(map[string]uint64, len(r.byRule))
	for k, v := range r.byRule {
		cp[k] = v
	}
	return Stats{
		Rules:       len(r.rules),
		Matches:     r.matches.Load(),
		Allowed:     r.allowedCount.Load(),
		ByRule:      cp,
		LastMatchAt: r.lastAt,
	}
}

// recordAllowed counts an exempted finding and notifies OnAllowed
// observers, so an allowlist pass-through is never silent.
func (r *Redactor) recordAllowed(rule *Rule, secret string, start, end int) {
	r.allowedCount.Add(1)
	r.obsMu.RLock()
	obs := r.allowedObs
	r.obsMu.RUnlock()
	if len(obs) == 0 {
		return
	}
	m := Match{RuleID: rule.spec.ID, Original: secret, Start: start, End: end}
	for _, fn := range obs {
		fn(m)
	}
}

func (r *Redactor) recordMatch(id string) {
	r.matches.Add(1)
	r.statsMu.Lock()
	r.byRule[id]++
	r.lastAt = time.Now()
	r.statsMu.Unlock()
}

func (r *Redactor) fireObservers(m Match) {
	r.obsMu.RLock()
	obs := r.observers
	r.obsMu.RUnlock()
	for _, fn := range obs {
		fn(m)
	}
}
