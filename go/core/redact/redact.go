// Package redact strips secrets, tokens, and PII from text before it leaves
// the process — log lines, telemetry payloads, LLM prompts, error reports,
// any user-facing output that may have absorbed sensitive data.
//
// The model:
//
//   - A Redactor holds an ordered list of compiled regex Rules and a single
//     Replacement strategy (Mask / Tag / Hash / Custom).
//   - Apply runs every rule's compiled pattern across the input via
//     regexp.ReplaceAllStringFunc, replaces each match per the strategy,
//     and fires registered observers.
//   - The engine is stdlib regexp (RE2). Linear-time matching is the entire
//     reason this can run on adversarial input — LLM outputs, scraped pages,
//     attacker-supplied prompts.
//
// Default() lazily loads the vendored gitleaks corpus + Presidio PII pack
// at first use.
package redact

import (
	"errors"
	"regexp"
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

// Match is emitted to observers for every successful redaction. Observer
// callbacks receive the matched text BEFORE replacement is applied.
type Match struct {
	RuleID      string
	Original    string
	Replacement string
	Start, End  int
}

// Stats is a snapshot of Redactor activity.
type Stats struct {
	Rules       int
	Matches     uint64
	ByRule      map[string]uint64
	LastMatchAt time.Time
}

// Rule is a compiled redaction pattern. Construct via NewRule or via the
// loader; never zero-value.
type Rule struct {
	id          string
	description string
	re          *regexp.Regexp
	// replacement is the rule-local template used by the Tag strategy when
	// non-empty (e.g. "<OPENAI_KEY>" overrides the default "<rule-id>").
	replacement string
	// allowlist holds rule-scoped substrings; matches containing any are
	// passed through unchanged. Global allowlist applies in addition.
	allowlist []string
}

// ID returns the rule identifier.
func (r Rule) ID() string { return r.id }

// Description returns the human-readable rule description.
func (r Rule) Description() string { return r.description }

// NewRule compiles pattern and returns a Rule. id must be non-empty;
// pattern must be RE2-clean. The optional replacement template is the
// rule-local label used by the Tag strategy (defaults to "<id>" when
// empty).
func NewRule(id, pattern, replacement string) (Rule, error) {
	if id == "" {
		return Rule{}, errors.New("redact: rule id is required")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Rule{}, err
	}
	return Rule{id: id, re: re, replacement: replacement}, nil
}

// Redactor applies a set of Rules to text, replacing matched substrings
// per its configured strategy. Safe for concurrent Apply / Scan / Stats
// once construction is complete; AddRule and SetReplacement are not
// concurrency-safe with Apply running.
type Redactor struct {
	rules    []Rule
	strategy Replacement
	custom   func(Match) string

	allowlist []string

	obsMu     sync.RWMutex
	observers []func(Match)

	matches atomic.Uint64
	statsMu sync.Mutex
	byRule  map[string]uint64
	lastAt  time.Time

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
// optional rule-local replacement template. Returns the receiver for
// chaining; on regex compile error returns nil and the error.
func (r *Redactor) AddRule(id, pattern, replacement string) (*Redactor, error) {
	rule, err := NewRule(id, pattern, replacement)
	if err != nil {
		return nil, err
	}
	r.rules = append(r.rules, rule)
	return r, nil
}

// AddRules appends pre-compiled rules in bulk. Used by the loader.
func (r *Redactor) AddRules(rules ...Rule) *Redactor {
	r.rules = append(r.rules, rules...)
	return r
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

// Allow adds substrings to the global allowlist. Any match whose Original
// contains an allowed substring is emitted unchanged with no observer fire.
func (r *Redactor) Allow(subs ...string) *Redactor {
	r.allowlist = append(r.allowlist, subs...)
	return r
}

// OnMatch registers an observer fired for every successful redaction (after
// allowlist filtering, before replacement substitution). Multiple calls
// chain in registration order.
func (r *Redactor) OnMatch(fn func(Match)) *Redactor {
	if fn == nil {
		r.obsMu.Lock()
		r.observers = nil
		r.obsMu.Unlock()
		return r
	}
	r.obsMu.Lock()
	r.observers = append(r.observers, fn)
	r.obsMu.Unlock()
	return r
}

// Apply returns s with every rule match replaced per the active strategy.
func (r *Redactor) Apply(s string) string {
	if len(r.rules) == 0 || s == "" {
		return s
	}
	out := s
	for i := range r.rules {
		rule := &r.rules[i]
		out = rule.re.ReplaceAllStringFunc(out, func(orig string) string {
			if r.allowed(rule, orig) {
				return orig
			}
			m := Match{
				RuleID:   rule.id,
				Original: orig,
				Start:    -1, // unknown after first rule pass; Scan reports positions
				End:      -1,
			}
			repl := r.format(m)
			m.Replacement = repl
			r.fireObservers(m)
			r.recordMatch(rule.id)
			return repl
		})
	}
	return out
}

// ApplyBytes is the []byte counterpart to Apply.
func (r *Redactor) ApplyBytes(b []byte) []byte {
	if len(r.rules) == 0 || len(b) == 0 {
		return b
	}
	out := b
	for i := range r.rules {
		rule := &r.rules[i]
		out = rule.re.ReplaceAllFunc(out, func(orig []byte) []byte {
			s := string(orig)
			if r.allowed(rule, s) {
				return orig
			}
			m := Match{RuleID: rule.id, Original: s, Start: -1, End: -1}
			repl := r.format(m)
			m.Replacement = repl
			r.fireObservers(m)
			r.recordMatch(rule.id)
			return []byte(repl)
		})
	}
	return out
}

// Scan returns every match without replacing or recording stats. Useful
// for audit-mode tools that need to know what would be redacted.
func (r *Redactor) Scan(s string) []Match {
	var out []Match
	for i := range r.rules {
		rule := &r.rules[i]
		idxs := rule.re.FindAllStringIndex(s, -1)
		for _, ix := range idxs {
			orig := s[ix[0]:ix[1]]
			if r.allowed(rule, orig) {
				continue
			}
			out = append(out, Match{
				RuleID:   rule.id,
				Original: orig,
				Start:    ix[0],
				End:      ix[1],
			})
		}
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
		ByRule:      cp,
		LastMatchAt: r.lastAt,
	}
}

// allowed reports whether orig contains any global or rule-scoped
// allowlist substring. String contains only — no regex on allowlists,
// to keep ReDoS risk strictly on the rules side.
func (r *Redactor) allowed(rule *Rule, orig string) bool {
	for _, s := range r.allowlist {
		if s != "" && contains(orig, s) {
			return true
		}
	}
	for _, s := range rule.allowlist {
		if s != "" && contains(orig, s) {
			return true
		}
	}
	return false
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

// contains is strings.Contains inlined to avoid the strings import in the
// hot path. (Equivalent semantics; the stdlib version specializes the
// same way.)
func contains(s, substr string) bool {
	if substr == "" {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
