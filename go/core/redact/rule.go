package redact

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// RuleSpec is a rule as data: what the loaders produce, what
// Rule.Spec returns and what a Policy carries. The field set follows
// the gitleaks rule schema; CompileRule turns it into a Rule.
type RuleSpec struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	// Regex is the RE2 pattern every match is found with.
	Regex string `json:"regex"`
	// Replacement is the label the Tag strategy writes; empty means
	// "<id>".
	Replacement string `json:"replacement,omitempty"`
	// SecretGroup names the capture group holding the secret. 0
	// redacts the whole match. The gitleaks loader resolves an unset
	// secretGroup to 1 when the pattern has exactly one group.
	SecretGroup int `json:"secret_group,omitempty"`
	// Entropy, when non-zero, drops a secret whose Shannon entropy is
	// at or below it.
	Entropy float64 `json:"entropy,omitempty"`
	// Keywords gate the rule: it runs only when the lower-cased input
	// contains one of them. None means it always runs.
	Keywords []string `json:"keywords,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	// Path is the gitleaks file-path scope. Text has no path, so it
	// is carried for reference and never consulted.
	Path       string          `json:"path,omitempty"`
	Allowlists []AllowlistSpec `json:"allowlists,omitempty"`
	// AllowlistExact holds literals compared against the whole secret
	// and the whole match.
	AllowlistExact []string `json:"allowlist_exact,omitempty"`
	// Distinctive is computed from Regex when the rule compiles (see
	// isDistinctive); a value set here is overwritten. Apply skips the
	// keyword gate and the entropy floor for distinctive rules.
	Distinctive bool `json:"distinctive"`
}

// AllowlistSpec is a gitleaks allowlist. With Condition "OR" (the
// default) a finding is exempt when a regex matches the target or the
// secret contains a stopword; with "AND" every populated check must
// hold. Paths and Commits never hold: text carries neither, so an AND
// list naming them never exempts anything.
//
// That is detection (Scan). Redaction (Apply, ApplyBytes, Spans) fails
// closed: stopwords never hold, and a regex holds only when it matches
// the whole target, not a substring of it.
type AllowlistSpec struct {
	Description string `json:"description,omitempty"`
	// Condition is "OR" or "AND"; empty reads as "OR".
	Condition string `json:"condition,omitempty"`
	// RegexTarget picks what Regexes test: "secret" (or empty), the
	// whole "match", or the "line" holding it.
	RegexTarget string   `json:"regex_target,omitempty"`
	Regexes     []string `json:"regexes,omitempty"`
	// StopWords exempt a secret containing one, case-insensitively.
	StopWords []string `json:"stopwords,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Commits   []string `json:"commits,omitempty"`
}

// Rule is a compiled redaction pattern. Construct via NewRule,
// CompileRule or a loader; never zero-value.
type Rule struct {
	spec       RuleSpec
	re         *regexp.Regexp
	allowlists []allowlist
}

// ID returns the rule identifier.
func (r Rule) ID() string { return r.spec.ID }

// Description returns the human-readable rule description.
func (r Rule) Description() string { return r.spec.Description }

// Pattern returns the regex source.
func (r Rule) Pattern() string { return r.spec.Regex }

// Regexp returns the compiled pattern. A *regexp.Regexp is safe for
// concurrent use; do not call Longest on it.
func (r Rule) Regexp() *regexp.Regexp { return r.re }

// SecretGroup returns the capture group redacted, 0 for the whole match.
func (r Rule) SecretGroup() int { return r.spec.SecretGroup }

// Entropy returns the minimum secret entropy, 0 when unchecked.
func (r Rule) Entropy() float64 { return r.spec.Entropy }

// Keywords returns a copy of the lower-cased keyword gate.
func (r Rule) Keywords() []string { return slices.Clone(r.spec.Keywords) }

// Tags returns a copy of the rule's tags.
func (r Rule) Tags() []string { return slices.Clone(r.spec.Tags) }

// Distinctive reports whether every match starts with a literal of at
// least 3 characters (ghp_, AKIA, sk-ant-). Apply redacts such a rule's
// matches without the keyword gate or the entropy floor; Scan keeps
// both.
func (r Rule) Distinctive() bool { return r.spec.Distinctive }

// Spec returns a deep copy of the rule as data.
func (r Rule) Spec() RuleSpec { return r.spec.clone() }

func (s RuleSpec) clone() RuleSpec {
	s.Keywords = slices.Clone(s.Keywords)
	s.Tags = slices.Clone(s.Tags)
	s.AllowlistExact = slices.Clone(s.AllowlistExact)
	if s.Allowlists != nil {
		al := make([]AllowlistSpec, len(s.Allowlists))
		for i, a := range s.Allowlists {
			al[i] = a.clone()
		}
		s.Allowlists = al
	}
	return s
}

func (a AllowlistSpec) clone() AllowlistSpec {
	a.Regexes = slices.Clone(a.Regexes)
	a.StopWords = slices.Clone(a.StopWords)
	a.Paths = slices.Clone(a.Paths)
	a.Commits = slices.Clone(a.Commits)
	return a
}

// NewRule compiles pattern and returns a Rule that redacts the whole
// match. id must be non-empty; pattern must be RE2-clean. The optional
// replacement template is the rule-local label used by the Tag
// strategy (defaults to "<id>" when empty).
func NewRule(id, pattern, replacement string) (Rule, error) {
	return CompileRule(RuleSpec{ID: id, Regex: pattern, Replacement: replacement})
}

// CompileRule validates spec and compiles its pattern and allowlists.
func CompileRule(spec RuleSpec) (Rule, error) {
	if spec.ID == "" {
		return Rule{}, errors.New("redact: rule id is required")
	}
	re, err := regexp.Compile(spec.Regex)
	if err != nil {
		return Rule{}, err
	}
	if spec.SecretGroup < 0 || spec.SecretGroup > re.NumSubexp() {
		return Rule{}, fmt.Errorf("redact: rule %s: secret group %d out of range 0..%d",
			spec.ID, spec.SecretGroup, re.NumSubexp())
	}
	if spec.Entropy < 0 || math.IsNaN(spec.Entropy) {
		return Rule{}, fmt.Errorf("redact: rule %s: invalid entropy %v", spec.ID, spec.Entropy)
	}
	spec = spec.clone()
	spec.Distinctive = isDistinctive(spec.Regex)
	for i, k := range spec.Keywords {
		if k == "" {
			return Rule{}, fmt.Errorf("redact: rule %s: empty keyword", spec.ID)
		}
		spec.Keywords[i] = strings.ToLower(k)
	}
	als, err := compileAllowlists(spec.Allowlists)
	if err != nil {
		return Rule{}, fmt.Errorf("redact: rule %s: %w", spec.ID, err)
	}
	return Rule{spec: spec, re: re, allowlists: als}, nil
}

// allowlist is a compiled AllowlistSpec.
type allowlist struct {
	and     bool
	target  string // "", "match" or "line"
	regexes []*regexp.Regexp
	// full holds each regex anchored to the whole target, for redaction.
	full      []*regexp.Regexp
	stopwords []string // lower-cased
	// contextual counts the Paths and Commits checks: never true here.
	contextual bool
}

func compileAllowlists(specs []AllowlistSpec) ([]allowlist, error) {
	out := make([]allowlist, 0, len(specs))
	for _, s := range specs {
		a, err := compileAllowlist(s)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func compileAllowlist(s AllowlistSpec) (allowlist, error) {
	var a allowlist
	switch strings.ToUpper(s.Condition) {
	case "", "OR", "||":
	case "AND", "&&":
		a.and = true
	default:
		return a, fmt.Errorf("allowlist: unknown condition %q", s.Condition)
	}
	switch s.RegexTarget {
	case "", "secret":
	case "match", "line":
		a.target = s.RegexTarget
	default:
		return a, fmt.Errorf("allowlist: unknown regex target %q", s.RegexTarget)
	}
	if len(s.Regexes) == 0 && len(s.StopWords) == 0 && len(s.Paths) == 0 && len(s.Commits) == 0 {
		return a, errors.New("allowlist: needs regexes, stopwords, paths or commits")
	}
	for _, p := range s.Regexes {
		re, err := regexp.Compile(p)
		if err != nil {
			return a, fmt.Errorf("allowlist regex %q: %w", p, err)
		}
		a.regexes = append(a.regexes, re)
		a.full = append(a.full, regexp.MustCompile(`\A(?:`+p+`)\z`))
	}
	for _, w := range s.StopWords {
		a.stopwords = append(a.stopwords, strings.ToLower(w))
	}
	a.contextual = len(s.Paths) > 0 || len(s.Commits) > 0
	return a, nil
}

// finding is what an allowlist judges: the secret, the whole match
// and the line(s) holding it.
type finding struct {
	secret, match, line string
}

// allows reports whether a exempts f. Detection follows gitleaks: each
// regex is tested on its own and must find a non-empty match, and a
// stopword anywhere in the secret holds. Redaction (detect false) fails
// closed: a regex must match the whole target and stopwords never hold.
func (a *allowlist) allows(f finding, detect bool) bool {
	target := f.secret
	switch a.target {
	case "match":
		target = f.match
	case "line":
		target = f.line
	}
	regexHit := false
	if target != "" {
		for i, re := range a.regexes {
			if !detect {
				if a.full[i].MatchString(target) {
					regexHit = true
					break
				}
				continue
			}
			if loc := re.FindStringIndex(target); loc != nil && loc[1] > loc[0] {
				regexHit = true
				break
			}
		}
	}
	stopHit := false
	if detect && f.secret != "" && len(a.stopwords) > 0 {
		lower := strings.ToLower(f.secret)
		for _, w := range a.stopwords {
			if strings.Contains(lower, w) {
				stopHit = true
				break
			}
		}
	}
	if !a.and {
		return regexHit || stopHit
	}
	if a.contextual {
		return false
	}
	if len(a.regexes) > 0 && !regexHit {
		return false
	}
	if len(a.stopwords) > 0 && !stopHit {
		return false
	}
	return true
}

// shannonEntropy matches gitleaks: frequencies are counted per rune
// but divided by the byte length, so multi-byte text scores lower than
// its rune count suggests. Terms are summed in ascending rune order
// (gitleaks sums in map order), so the value is reproducible to the
// bit and ports can sum the same way.
func shannonEntropy(data string) float64 {
	if data == "" {
		return 0
	}
	runes := []rune(data)
	slices.Sort(runes)
	inv := 1.0 / float64(len(data))
	var e float64
	for i := 0; i < len(runes); {
		j := i + 1
		for j < len(runes) && runes[j] == runes[i] {
			j++
		}
		f := float64(j-i) * inv
		e -= f * math.Log2(f)
		i = j
	}
	return e
}
