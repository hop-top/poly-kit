// Package policy implements the AI-harness permission policy table
// described in ADR-0019. A Table maps a tool's risk metadata
// (side-effect class × network axis) to one of three actions:
// auto-allow, prompt, deny.
//
// Kit ships an opinionated default table as embedded YAML
// (default.yaml in this directory). Harnesses load it via
// policy.Default(). Adopters override or extend by loading their
// own YAML and merging with policy.Merge().
//
// The contract is intentionally small. Policy resolution is
// deterministic, side-effect-free, and stateless — the same input
// always produces the same Decision. This makes the policy easy
// to test (every (side_effect, network) tuple has a documented
// outcome) and easy to audit (a single rule table is all the
// gatekeeper needs to consult).
//
// Today's vocabulary mirrors the existing kit/side-effect enum
// (read, write, destructive, interactive) and a 3-value network
// axis (none, local-only, egress) plus the wildcard "any".
// kit-toolspec-safety-ladder will populate the network axis on
// every command and may expand the side-effect enum; the schema
// version on the table tracks both.
package policy

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// SideEffect is the side-effect class read from a command's
// kit/side-effect annotation. Mirrors go/console/cli.SideEffect
// (kept duplicated to avoid an import cycle: policy → cli would
// create one, cli → policy would too once cli wires policy in).
type SideEffect string

const (
	SideEffectRead        SideEffect = "read"
	SideEffectWrite       SideEffect = "write"
	SideEffectDestructive SideEffect = "destructive"
	SideEffectInteractive SideEffect = "interactive"
)

// Network is the network axis read from a command's kit/network
// annotation. Currently a stub: kit-toolspec-safety-ladder will
// land the annotation; until then, every command resolves at
// NetworkNone (no kit/network annotation = "no I/O").
type Network string

const (
	// NetworkNone marks a command that performs no network I/O.
	NetworkNone Network = "none"
	// NetworkLocalOnly marks a command that talks to local
	// daemons or sockets but never crosses a NIC boundary.
	NetworkLocalOnly Network = "local-only"
	// NetworkEgress marks a command that hits arbitrary remote
	// hosts (git push, curl, npm install).
	NetworkEgress Network = "egress"
	// NetworkAny is the wildcard used in policy rules for the
	// "applies to every network value" row (e.g. interactive).
	NetworkAny Network = "any"
)

// Action is the policy decision the harness should apply.
type Action string

const (
	// ActionAutoAllow means the harness invokes the tool without
	// prompting. Equivalent to a permission allowlist entry.
	ActionAutoAllow Action = "auto-allow"
	// ActionPrompt means the harness asks the user before
	// invocation, surfacing the resolved policy reason.
	ActionPrompt Action = "prompt"
	// ActionDeny means the harness refuses with a clear reason;
	// the user must explicitly override.
	ActionDeny Action = "deny"
)

// Decision is a resolved policy outcome with the human-readable
// reason attached so harnesses can render WHY a tool was gated.
type Decision struct {
	// Action is the resolved policy action.
	Action Action `json:"action"`
	// Reason is the rule's human-readable rationale, surfaced to
	// the user when the harness prompts or denies.
	Reason string `json:"reason"`
	// Source identifies which rule produced the decision —
	// useful for debugging policy-table merges.
	Source string `json:"source,omitempty"`
}

// Rule is one row in the policy table. SideEffect MUST be
// non-empty; Network may be NetworkAny to cover every column.
// Action and Reason are required; Source is filled in at load
// time so reverse-lookups can attribute decisions.
type Rule struct {
	SideEffect SideEffect `yaml:"side_effect" json:"side_effect"`
	Network    Network    `yaml:"network" json:"network"`
	Action     Action     `yaml:"action" json:"action"`
	Reason     string     `yaml:"reason" json:"reason"`
	// Source is set by the loader (e.g. "default.yaml" or a
	// file path); not part of the YAML schema, ignored on
	// unmarshal.
	Source string `yaml:"-" json:"source,omitempty"`
}

// Table is the full policy table. SchemaVersion locks the file
// layout; Rules carries the actual mappings.
type Table struct {
	SchemaVersion string `yaml:"schema_version" json:"schema_version"`
	Rules         []Rule `yaml:"rules" json:"rules"`
}

//go:embed default.yaml
var defaultYAML []byte

// Default returns the embedded default policy table parsed at
// startup. Safe to mutate the returned value (it is a copy);
// repeated calls return fresh copies.
func Default() Table {
	var t Table
	if err := yaml.Unmarshal(defaultYAML, &t); err != nil {
		// The embed is part of the binary — failing to parse it
		// is a build/programmer error, not a runtime concern.
		panic(fmt.Sprintf("policy: parse embedded default.yaml: %v", err))
	}
	for i := range t.Rules {
		t.Rules[i].Source = "default.yaml"
	}
	return t
}

// Load parses a YAML policy table from r. Returns a typed Table
// or an error if the YAML is malformed. The returned table's
// rules carry Source = source so Decision.Source attribution is
// useful when overlaying onto the default.
func Load(r io.Reader, source string) (Table, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return Table{}, fmt.Errorf("policy: read %s: %w", source, err)
	}
	var t Table
	if err := yaml.Unmarshal(buf, &t); err != nil {
		return Table{}, fmt.Errorf("policy: parse %s: %w", source, err)
	}
	for i := range t.Rules {
		t.Rules[i].Source = source
	}
	return t, nil
}

// LoadBytes is the byte-slice convenience wrapper around Load.
func LoadBytes(b []byte, source string) (Table, error) {
	return Load(bytes.NewReader(b), source)
}

// Resolve returns the Decision for the given (side_effect, network)
// tuple. Resolution order:
//
//  1. Exact (SideEffect, Network) match wins.
//  2. (SideEffect, NetworkAny) catches column-spanning rules.
//  3. No match → ActionPrompt with a "no rule matched" reason
//     (fail-safe default; harnesses NEVER auto-allow unmapped
//     tuples).
//
// Repeated rules with the same key: the first wins (so overlays
// via Merge can prepend their custom rules to override defaults).
func (t Table) Resolve(se SideEffect, net Network) Decision {
	// Pass 1: exact match.
	for _, r := range t.Rules {
		if r.SideEffect == se && r.Network == net {
			return Decision{Action: r.Action, Reason: r.Reason, Source: r.Source}
		}
	}
	// Pass 2: side-effect with NetworkAny wildcard.
	for _, r := range t.Rules {
		if r.SideEffect == se && r.Network == NetworkAny {
			return Decision{Action: r.Action, Reason: r.Reason, Source: r.Source}
		}
	}
	// Fail-safe default: prompt with attribution.
	return Decision{
		Action: ActionPrompt,
		Reason: fmt.Sprintf("no rule matched (side_effect=%q, network=%q); fail-safe prompt", se, net),
		Source: "fallback",
	}
}

// Merge overlays overlay onto base — overlay rules win on (side_effect,
// network) collisions, base rules cover the rest. Used by the MCP
// adapter's --policy <file> flag (T-0497): the user-supplied YAML
// merges over the embedded default. SchemaVersion comes from overlay
// when set, else from base; mismatches are caller-visible (Decision's
// Source attribution shows which table the winning rule came from).
//
// Non-mutating: returns a fresh Table; base and overlay are unchanged.
func Merge(base, overlay Table) Table {
	merged := Table{
		SchemaVersion: base.SchemaVersion,
		Rules:         make([]Rule, 0, len(base.Rules)+len(overlay.Rules)),
	}
	if overlay.SchemaVersion != "" {
		merged.SchemaVersion = overlay.SchemaVersion
	}
	// Overlay rules first so Resolve's first-match wins gives
	// them priority.
	merged.Rules = append(merged.Rules, overlay.Rules...)
	// Base rules append, but skip any (side_effect, network) the
	// overlay already covered — purely cosmetic dedup, doesn't
	// affect Resolve since overlay rules are already first.
	for _, b := range base.Rules {
		if hasRule(overlay.Rules, b.SideEffect, b.Network) {
			continue
		}
		merged.Rules = append(merged.Rules, b)
	}
	return merged
}

// hasRule reports whether rules contains a rule keyed on (se, net).
func hasRule(rules []Rule, se SideEffect, net Network) bool {
	for _, r := range rules {
		if r.SideEffect == se && r.Network == net {
			return true
		}
	}
	return false
}
