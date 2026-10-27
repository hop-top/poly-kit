package redact

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	scoperules "hop.top/kit/go/core/scope/rules"
)

//go:embed rules/presidio-pii.toml
var embeddedPresidio []byte

// embedSentinel signals "use the embedded bytes" rather than a real path.
// DefaultGitleaksPath returns this when KIT_REDACT_RULES_PATH is unset so
// LoadGitleaks knows to read from the embed.
const embedSentinel = "embed:"

// gitleaksFile mirrors the gitleaks config schema the engine honors:
// rules with keywords, secretGroup, entropy and allowlists, plus the
// global [allowlist] / [[allowlists]] (targetRules included). Composite
// rules ([[rules.required]]) and skipReport are not modeled.
type gitleaksFile struct {
	Allowlist  *gitleaksAllowlist  `toml:"allowlist"`
	Allowlists []gitleaksAllowlist `toml:"allowlists"`
	Rules      []gitleaksRule      `toml:"rules"`
}

type gitleaksAllowlist struct {
	Description string   `toml:"description"`
	Condition   string   `toml:"condition"`
	RegexTarget string   `toml:"regexTarget"`
	Regexes     []string `toml:"regexes"`
	StopWords   []string `toml:"stopwords"`
	Paths       []string `toml:"paths"`
	Commits     []string `toml:"commits"`
	TargetRules []string `toml:"targetRules"`
}

func (a gitleaksAllowlist) spec() AllowlistSpec {
	return AllowlistSpec{
		Description: a.Description, Condition: a.Condition, RegexTarget: a.RegexTarget,
		Regexes: a.Regexes, StopWords: a.StopWords, Paths: a.Paths, Commits: a.Commits,
	}
}

type gitleaksRule struct {
	ID          string   `toml:"id"`
	Description string   `toml:"description"`
	Regex       string   `toml:"regex"`
	SecretGroup int      `toml:"secretGroup"`
	Entropy     float64  `toml:"entropy"`
	Keywords    []string `toml:"keywords"`
	Tags        []string `toml:"tags"`
	// Path scopes a rule to files. Path-only rules are dropped; on
	// regex rules it is kept as data and never consulted.
	Path       string              `toml:"path"`
	Allowlist  *gitleaksAllowlist  `toml:"allowlist"`
	Allowlists []gitleaksAllowlist `toml:"allowlists"`
}

// corpusPatch is a kit-side fix to the vendored corpus, applied at
// load time so the vendored file stays verbatim. rule "" targets the
// global allowlists.
type corpusPatch struct {
	rule, old, new string
}

// gitleaksPatches close upstream allowlist entries that exempt live
// secrets. A patch whose text is absent (a custom corpus, a refreshed
// upstream) is skipped; TestGitleaksPatchesApply pins the vendored one.
var gitleaksPatches = []corpusPatch{
	// The alternation escaped its anchors: any secret containing
	// "false" anywhere was exempt.
	{"", `(?i)^true|false|null$`, `(?i)^(?:true|false|null)$`},
	// "author" also matches "Authorization"; "primary key" names real
	// credentials (Azure storage PrimaryKey).
	{"generic-api-key", `|author|X-MS-Exchange-Organization-Auth|`, `|X-MS-Exchange-Organization-Auth|`},
	{"generic-api-key", `|natural|primary|pub(?:lic)?|`, `|natural|pub(?:lic)?|`},
}

// applyPatches rewrites allowlist regexes in place and returns how
// many patches found their text.
func applyPatches(global []AllowlistSpec, rules []RuleSpec, patches []corpusPatch) int {
	applied := 0
	for _, p := range patches {
		lists := global
		if p.rule != "" {
			lists = nil
			for i := range rules {
				if rules[i].ID == p.rule {
					lists = rules[i].Allowlists
				}
			}
		}
		hit := false
		for i := range lists {
			for j, re := range lists[i].Regexes {
				if strings.Contains(re, p.old) {
					lists[i].Regexes[j] = strings.Replace(re, p.old, p.new, 1)
					hit = true
				}
			}
		}
		if hit {
			applied++
		}
	}
	return applied
}

func readRules(path string, embedded []byte, kind string) ([]byte, error) {
	if path == embedSentinel {
		return embedded, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("redact: read %s rules %q: %w", kind, path, err)
	}
	return b, nil
}

// LoadGitleaksPolicy parses the gitleaks TOML at path (the embedded
// copy when path is the embed sentinel) into a Policy: its rules and
// its global allowlists, with the kit corpus patches applied. Rules
// whose regex does not compile under RE2 are skipped; the vendored
// corpus is RE2-clean (verified in test). An unset secretGroup
// resolves to 1 when the pattern has exactly one capture group, and to
// the whole match otherwise.
func LoadGitleaksPolicy(path string) (Policy, error) {
	data, err := readRules(path, scoperules.GitleaksContent, "gitleaks")
	if err != nil {
		return Policy{}, err
	}
	p, err := parseGitleaks(data)
	if err != nil {
		return Policy{}, err
	}
	applyPatches(p.Allowlists, p.Rules, gitleaksPatches)
	return p, nil
}

// parseGitleaks converts gitleaks TOML to a Policy, unpatched.
func parseGitleaks(data []byte) (Policy, error) {
	var f gitleaksFile
	if _, err := toml.Decode(string(data), &f); err != nil {
		return Policy{}, fmt.Errorf("redact: parse gitleaks rules: %w", err)
	}
	p := Policy{Version: PolicyVersion}
	targeted := map[string][]AllowlistSpec{}
	globals := f.Allowlists
	if f.Allowlist != nil {
		globals = append([]gitleaksAllowlist{*f.Allowlist}, globals...)
	}
	for _, a := range globals {
		if len(a.TargetRules) == 0 {
			p.Allowlists = append(p.Allowlists, a.spec())
			continue
		}
		for _, id := range a.TargetRules {
			targeted[id] = append(targeted[id], a.spec())
		}
	}
	for _, gr := range f.Rules {
		if gr.ID == "" || gr.Regex == "" {
			continue // path-only entries or malformed
		}
		re, err := regexp.Compile(gr.Regex)
		if err != nil {
			continue // RE2-incompatible upstream rule
		}
		spec := RuleSpec{
			ID: gr.ID, Description: gr.Description, Regex: gr.Regex,
			SecretGroup: gr.SecretGroup, Entropy: gr.Entropy,
			Keywords: gr.Keywords, Tags: gr.Tags, Path: gr.Path,
		}
		if spec.SecretGroup == 0 && re.NumSubexp() == 1 {
			spec.SecretGroup = 1
		}
		if gr.Allowlist != nil {
			spec.Allowlists = append(spec.Allowlists, gr.Allowlist.spec())
		}
		for _, a := range gr.Allowlists {
			spec.Allowlists = append(spec.Allowlists, a.spec())
		}
		spec.Allowlists = append(spec.Allowlists, targeted[gr.ID]...)
		p.Rules = append(p.Rules, spec)
	}
	return p, nil
}

// LoadGitleaks returns the compiled rules of the gitleaks TOML at
// path; see LoadGitleaksPolicy. The global allowlists are not part of
// the result: install them with Redactor.AddAllowlists, or build from
// the whole policy with NewFromPolicy.
func LoadGitleaks(path string) ([]Rule, error) {
	p, err := LoadGitleaksPolicy(path)
	if err != nil {
		return nil, err
	}
	return compileSpecs(p.Rules, true)
}

// compileSpecs compiles specs; lenient skips the ones that fail.
func compileSpecs(specs []RuleSpec, lenient bool) ([]Rule, error) {
	out := make([]Rule, 0, len(specs))
	for _, s := range specs {
		rule, err := CompileRule(s)
		if err != nil {
			if lenient {
				continue
			}
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

// DefaultGitleaksPath returns the path LoadGitleaks should read by
// default. Honors KIT_REDACT_RULES_PATH for ops overrides; otherwise
// returns the embed sentinel so the embedded vendored copy is used.
func DefaultGitleaksPath() string {
	if p := os.Getenv("KIT_REDACT_RULES_PATH"); p != "" {
		return p
	}
	return embedSentinel
}

// presidioFile mirrors rules/presidio-pii.toml. Schema uses [[rule]]
// (singular) per the kit-native spec; keep it distinct from the gitleaks
// [[rules]] schema we don't own.
type presidioFile struct {
	Rule []presidioRule `toml:"rule"`
}

type presidioRule struct {
	ID          string `toml:"id"`
	Description string `toml:"description"`
	Pattern     string `toml:"pattern"`
	Replacement string `toml:"replacement"`
	// AllowlistExact is compared against the whole match. The former
	// substring-matched `allowlist` key was removed: a match embedding an
	// entry escaped redaction entirely.
	AllowlistExact []string `toml:"allowlist_exact"`
	MinConfidence  float64  `toml:"min_confidence"`
}

// loadPresidioSpecs parses the Presidio TOML at path. Presidio rules
// redact the whole match.
func loadPresidioSpecs(path string) ([]RuleSpec, error) {
	data, err := readRules(path, embeddedPresidio, "presidio")
	if err != nil {
		return nil, err
	}
	var f presidioFile
	if _, err := toml.Decode(string(data), &f); err != nil {
		return nil, fmt.Errorf("redact: parse presidio rules: %w", err)
	}
	out := make([]RuleSpec, 0, len(f.Rule))
	for _, pr := range f.Rule {
		if pr.ID == "" || pr.Pattern == "" {
			continue
		}
		out = append(out, RuleSpec{
			ID: pr.ID, Description: pr.Description, Regex: pr.Pattern,
			Replacement: pr.Replacement, AllowlistExact: pr.AllowlistExact,
		})
	}
	return out, nil
}

// LoadPresidio parses the vendored Presidio PII TOML at path. Same
// semantics as LoadGitleaks. RE2-incompatible patterns are skipped
// silently (the corpus is curated to be RE2-clean).
func LoadPresidio(path string) ([]Rule, error) {
	specs, err := loadPresidioSpecs(path)
	if err != nil {
		return nil, err
	}
	return compileSpecs(specs, true)
}

// DefaultPresidioPath returns the path LoadPresidio should read by
// default. Honors KIT_REDACT_PII_RULES_PATH for ops overrides;
// otherwise returns the embed sentinel.
func DefaultPresidioPath() string {
	if p := os.Getenv("KIT_REDACT_PII_RULES_PATH"); p != "" {
		return p
	}
	return embedSentinel
}

// defaultOnce guards Default() initialization.
var (
	defaultOnce sync.Once
	defaultRdc  *Redactor
)

// Default returns the package-singleton Redactor. First call eagerly loads
// the vendored gitleaks rule corpus, its global allowlists and the
// Presidio PII pack, honoring KIT_REDACT_RULES_PATH and
// KIT_REDACT_PII_RULES_PATH. Subsequent calls return the same instance.
// NewEmbedded builds the same policy from the embedded corpora only.
//
// Rule-id collision policy: gitleaks loads first and wins. Conflicting
// Presidio rule ids get the suffix "-pii" appended at load time.
//
// On parse error: panics. Broken vendored rules are a build-time bug,
// not a runtime decision.
func Default() *Redactor {
	defaultOnce.Do(func() {
		p, err := buildPolicy(DefaultGitleaksPath(), DefaultPresidioPath())
		if err != nil {
			panic(fmt.Sprintf("redact.Default: %v", err))
		}
		r, err := NewFromPolicy(p)
		if err != nil {
			panic(fmt.Sprintf("redact.Default: %v", err))
		}
		defaultRdc = r
	})
	return defaultRdc
}

// buildPolicy assembles the default policy: gitleaks rules and global
// allowlists, then Presidio rules with colliding ids suffixed "-pii".
func buildPolicy(gitleaksPath, presidioPath string) (Policy, error) {
	p, err := LoadGitleaksPolicy(gitleaksPath)
	if err != nil {
		return Policy{}, fmt.Errorf("load gitleaks rules: %w", err)
	}
	seen := make(map[string]bool, len(p.Rules))
	for _, s := range p.Rules {
		seen[s.ID] = true
	}
	pii, err := loadPresidioSpecs(presidioPath)
	if err != nil {
		return Policy{}, fmt.Errorf("load presidio rules: %w", err)
	}
	for _, s := range pii {
		if _, err := regexp.Compile(s.Regex); err != nil {
			continue
		}
		if seen[s.ID] {
			if s.Replacement == "<"+s.ID+">" {
				s.Replacement = ""
			}
			s.ID += "-pii"
		}
		seen[s.ID] = true
		p.Rules = append(p.Rules, s)
	}
	return p, nil
}
