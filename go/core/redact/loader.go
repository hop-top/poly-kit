package redact

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
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

// gitleaksFile mirrors the subset of the gitleaks TOML schema we care
// about. Per-rule [[rules.allowlists]] are intentionally NOT modeled —
// gitleaks allowlists are regex-based and the redact engine's allowlist
// is substring-only by design (see Allow + Rule.allowlist). Tools that
// need to whitelist common test fixtures (sk-test, AKIA...EXAMPLE) do so
// via Redactor.Allow().
type gitleaksFile struct {
	Rules []gitleaksRule `toml:"rules"`
}

type gitleaksRule struct {
	ID          string  `toml:"id"`
	Description string  `toml:"description"`
	Regex       string  `toml:"regex"`
	Entropy     float64 `toml:"entropy"`
	// Path is set on path-allowlist rules upstream. We ignore those:
	// they belong to kit/scope, not content matching.
	Path string `toml:"path"`
}

// LoadGitleaks parses the vendored gitleaks TOML at path and returns the
// converted []Rule. When path == embedSentinel the embedded copy is
// parsed instead. Rules whose regex fails to compile under RE2 are
// skipped silently (the corpus is RE2-clean in practice; verify in test).
//
// Replacement template defaults to "<{rule-id}>" so the Tag strategy
// produces diagnosable output (e.g. "<openai-api-key>").
func LoadGitleaks(path string) ([]Rule, error) {
	var data []byte
	if path == embedSentinel {
		data = scoperules.GitleaksContent
	} else {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("redact: read gitleaks rules %q: %w", path, err)
		}
		data = b
	}

	var f gitleaksFile
	if _, err := toml.Decode(string(data), &f); err != nil {
		return nil, fmt.Errorf("redact: parse gitleaks rules: %w", err)
	}

	out := make([]Rule, 0, len(f.Rules))
	for _, gr := range f.Rules {
		if gr.ID == "" || gr.Regex == "" {
			continue // path-only entries or malformed
		}
		re, err := regexp.Compile(gr.Regex)
		if err != nil {
			// RE2-incompatible upstream rule. Skip rather than fail —
			// loss of one rule beats refusing to load the corpus.
			continue
		}
		out = append(out, Rule{
			id:          gr.ID,
			description: gr.Description,
			re:          re,
			replacement: "<" + gr.ID + ">",
		})
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
	ID            string   `toml:"id"`
	Description   string   `toml:"description"`
	Pattern       string   `toml:"pattern"`
	Replacement   string   `toml:"replacement"`
	Allowlist     []string `toml:"allowlist"`
	MinConfidence float64  `toml:"min_confidence"`
}

// LoadPresidio parses the vendored Presidio PII TOML at path. Same
// semantics as LoadGitleaks. RE2-incompatible patterns are skipped
// silently (the corpus is curated to be RE2-clean).
func LoadPresidio(path string) ([]Rule, error) {
	var data []byte
	if path == embedSentinel {
		data = embeddedPresidio
	} else {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("redact: read presidio rules %q: %w", path, err)
		}
		data = b
	}

	var f presidioFile
	if _, err := toml.Decode(string(data), &f); err != nil {
		return nil, fmt.Errorf("redact: parse presidio rules: %w", err)
	}

	out := make([]Rule, 0, len(f.Rule))
	for _, pr := range f.Rule {
		if pr.ID == "" || pr.Pattern == "" {
			continue
		}
		re, err := regexp.Compile(pr.Pattern)
		if err != nil {
			continue
		}
		repl := pr.Replacement
		if repl == "" {
			repl = "<" + pr.ID + ">"
		}
		out = append(out, Rule{
			id:          pr.ID,
			description: pr.Description,
			re:          re,
			replacement: repl,
			allowlist:   pr.Allowlist,
		})
	}
	return out, nil
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
// the vendored gitleaks rule corpus and the Presidio PII pack.
// Subsequent calls return the same instance.
//
// Rule-id collision policy: gitleaks loads first and wins. Conflicting
// Presidio rule ids get the suffix "-pii" appended at load time.
//
// On parse error: panics. Broken vendored rules are a build-time bug,
// not a runtime decision.
func Default() *Redactor {
	defaultOnce.Do(func() {
		r := New()
		gl, err := LoadGitleaks(DefaultGitleaksPath())
		if err != nil {
			panic(fmt.Sprintf("redact.Default: load gitleaks rules: %v", err))
		}
		r.AddRules(gl...)

		seen := make(map[string]bool, len(gl))
		for _, rule := range gl {
			seen[rule.id] = true
		}

		pii, err := LoadPresidio(DefaultPresidioPath())
		if err != nil {
			panic(fmt.Sprintf("redact.Default: load presidio rules: %v", err))
		}
		for i := range pii {
			if seen[pii[i].id] {
				pii[i].id += "-pii"
				if pii[i].replacement == "<"+pii[i].id[:len(pii[i].id)-4]+">" {
					pii[i].replacement = "<" + pii[i].id + ">"
				}
			}
			seen[pii[i].id] = true
		}
		r.AddRules(pii...)
		defaultRdc = r
	})
	return defaultRdc
}
