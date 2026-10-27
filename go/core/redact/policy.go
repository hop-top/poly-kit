package redact

import (
	"fmt"
	"slices"
)

// PolicyVersion is the Policy format this package reads and writes.
const PolicyVersion = "v1"

// Policy is a whole Redactor configuration as data: global allowlists
// and exact entries, then rules in evaluation order. It is the format
// of contracts/redact-v1/rules.json, which the SDK ports embed.
type Policy struct {
	Version        string          `json:"version"`
	Allowlists     []AllowlistSpec `json:"allowlists,omitempty"`
	AllowlistExact []string        `json:"allowlist_exact,omitempty"`
	Rules          []RuleSpec      `json:"rules"`
}

// NewFromPolicy builds a Redactor (Mask strategy) from p. Unlike the
// TOML loaders it is strict: any rule or allowlist that does not
// compile fails the call.
func NewFromPolicy(p Policy, opts ...Option) (*Redactor, error) {
	if p.Version != "" && p.Version != PolicyVersion {
		return nil, fmt.Errorf("redact: policy version %q, want %q", p.Version, PolicyVersion)
	}
	rules, err := compileSpecs(p.Rules, false)
	if err != nil {
		return nil, err
	}
	r := New(opts...)
	if _, err := r.AddAllowlists(p.Allowlists...); err != nil {
		return nil, fmt.Errorf("redact: global allowlist: %w", err)
	}
	r.AllowExact(p.AllowlistExact...)
	return r.AddRules(rules...), nil
}

// Policy returns r's configuration as data. Strategy and observers
// are not part of it.
func (r *Redactor) Policy() Policy {
	p := Policy{Version: PolicyVersion, AllowlistExact: slices.Clone(r.exactAllowlist)}
	for _, a := range r.allowlistSpecs {
		p.Allowlists = append(p.Allowlists, a.clone())
	}
	p.Rules = make([]RuleSpec, len(r.rules))
	for i := range r.rules {
		p.Rules[i] = r.rules[i].spec.clone()
	}
	return p
}

// EmbeddedPolicy returns the default policy built from the embedded
// corpora only: KIT_REDACT_RULES_PATH and KIT_REDACT_PII_RULES_PATH
// are never consulted.
func EmbeddedPolicy() (Policy, error) {
	return buildPolicy(embedSentinel, embedSentinel)
}

// NewEmbedded returns a fresh Redactor with the embedded default
// policy, ignoring the rule-path environment overrides. Use it where
// an operator-supplied corpus must not replace the vendored one.
func NewEmbedded(opts ...Option) (*Redactor, error) {
	p, err := EmbeddedPolicy()
	if err != nil {
		return nil, err
	}
	return NewFromPolicy(p, opts...)
}
