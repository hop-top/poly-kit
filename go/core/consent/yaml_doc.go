package consent

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// This file isolates the yaml.Node surgery from the Store I/O so the
// FileStore reads as plain "read, transform, write". The transforms
// preserve sibling top-level keys (the file is the kit AppConfig, not
// consent-only) and replace only the kit.telemetry.consent block on
// Set / Clear.
//
// The canonical path is kit.telemetry.consent inside config.yaml; the
// pre-refactor layout used bare telemetry.consent inside a dedicated
// telemetry.yaml. extractLegacyDecision walks the old shape so the
// read path can fall back during migration.

const (
	keyKit            = "kit"
	keyTelemetry      = "telemetry"
	keyConsent        = "consent"
	keyState          = "state"
	keyDecidedAt      = "decided_at"
	keyPromptVersion  = "prompt_version"
	keyDecisionSource = "decision_source"
)

// newMappingDoc returns a fresh document node whose root is an empty
// mapping. Used when Set is called against a missing file.
func newMappingDoc() *yaml.Node {
	return &yaml.Node{
		Kind: yaml.DocumentNode,
		Content: []*yaml.Node{
			{Kind: yaml.MappingNode, Tag: "!!map"},
		},
	}
}

// rootMapping returns the top-level mapping node of doc, or nil if the
// document is empty / not a mapping. We do not auto-coerce a non-
// mapping root because that would silently drop user content.
func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc == nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

// mappingChild returns the value node under the given key in a mapping
// node, or nil if absent. Mapping nodes alternate key/value pairs in
// Content; we iterate in pairs.
func mappingChild(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setMappingChild replaces (or inserts) the (key, value) pair on m.
// Appending preserves the existing order of sibling keys, which is the
// behavior we want — `other: foo` written before consent stays put.
func setMappingChild(m *yaml.Node, key string, value *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

// deleteMappingChild removes the (key, value) pair from m, if present.
// No-op if absent — Clear on an unset key is a success.
func deleteMappingChild(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// extractDecision walks doc looking for kit.telemetry.consent and
// decodes it into a Decision. Returns (Decision{}, false, nil) when
// the block is absent or empty; (_, false, err) on malformed scalars.
func extractDecision(doc *yaml.Node) (Decision, bool, error) {
	root := rootMapping(doc)
	if root == nil {
		return Decision{}, false, nil
	}
	kit := mappingChild(root, keyKit)
	if kit == nil || kit.Kind != yaml.MappingNode {
		return Decision{}, false, nil
	}
	telemetry := mappingChild(kit, keyTelemetry)
	if telemetry == nil || telemetry.Kind != yaml.MappingNode {
		return Decision{}, false, nil
	}
	consent := mappingChild(telemetry, keyConsent)
	if consent == nil || consent.Kind != yaml.MappingNode {
		return Decision{}, false, nil
	}
	return decodeConsentMapping(consent)
}

// extractLegacyDecision walks the pre-refactor doc shape
// (telemetry.consent at the top level, no kit wrapper). Same return
// contract as extractDecision; only the navigation differs.
func extractLegacyDecision(doc *yaml.Node) (Decision, bool, error) {
	root := rootMapping(doc)
	if root == nil {
		return Decision{}, false, nil
	}
	telemetry := mappingChild(root, keyTelemetry)
	if telemetry == nil || telemetry.Kind != yaml.MappingNode {
		return Decision{}, false, nil
	}
	consent := mappingChild(telemetry, keyConsent)
	if consent == nil || consent.Kind != yaml.MappingNode {
		return Decision{}, false, nil
	}
	return decodeConsentMapping(consent)
}

// decodeConsentMapping decodes a consent: mapping into a Decision.
// Shared between the canonical kit.telemetry.consent path and the
// legacy telemetry.consent path so both walks produce identical
// Decision values.
func decodeConsentMapping(consent *yaml.Node) (Decision, bool, error) {
	var d Decision
	if v := mappingChild(consent, keyState); v != nil {
		d.State = State(v.Value)
	}
	if v := mappingChild(consent, keyDecidedAt); v != nil && v.Value != "" {
		t, err := time.Parse(time.RFC3339, v.Value)
		if err != nil {
			return Decision{}, false, fmt.Errorf(
				"consent: parse decided_at %q: %w", v.Value, err,
			)
		}
		d.DecidedAt = t
	}
	if v := mappingChild(consent, keyPromptVersion); v != nil && v.Value != "" {
		var pv int
		if _, err := fmt.Sscanf(v.Value, "%d", &pv); err != nil {
			return Decision{}, false, fmt.Errorf(
				"consent: parse prompt_version %q: %w", v.Value, err,
			)
		}
		d.PromptVersion = pv
	}
	if v := mappingChild(consent, keyDecisionSource); v != nil {
		d.DecisionSource = DecisionSource(v.Value)
	}
	return d, true, nil
}

// upsertDecision writes d into doc under kit.telemetry.consent,
// creating intermediate mappings as needed. Other top-level keys (and
// other keys under kit / kit.telemetry, should any exist) are
// preserved.
func upsertDecision(doc *yaml.Node, d Decision) error {
	root := rootMapping(doc)
	if root == nil {
		return fmt.Errorf("consent: document root is not a mapping")
	}

	kit := mappingChild(root, keyKit)
	if kit == nil || kit.Kind != yaml.MappingNode {
		kit = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingChild(root, keyKit, kit)
	}

	telemetry := mappingChild(kit, keyTelemetry)
	if telemetry == nil || telemetry.Kind != yaml.MappingNode {
		telemetry = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingChild(kit, keyTelemetry, telemetry)
	}

	consent := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	scalar := func(v string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	}
	intScalar := func(v int) *yaml.Node {
		return &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!int",
			Value: fmt.Sprintf("%d", v),
		}
	}
	setMappingChild(consent, keyState, scalar(string(d.State)))
	setMappingChild(consent, keyDecidedAt,
		scalar(d.DecidedAt.UTC().Format(time.RFC3339)))
	setMappingChild(consent, keyPromptVersion, intScalar(d.PromptVersion))
	setMappingChild(consent, keyDecisionSource,
		scalar(string(d.DecisionSource)))

	setMappingChild(telemetry, keyConsent, consent)
	return nil
}

// removeConsent strips the kit.telemetry.consent block. If
// kit.telemetry becomes empty after the removal we leave it in place:
// a present-but-empty mapping is harmless and avoids racing with any
// future kit.telemetry sub-keys.
func removeConsent(doc *yaml.Node) {
	root := rootMapping(doc)
	if root == nil {
		return
	}
	kit := mappingChild(root, keyKit)
	if kit == nil || kit.Kind != yaml.MappingNode {
		return
	}
	telemetry := mappingChild(kit, keyTelemetry)
	if telemetry == nil || telemetry.Kind != yaml.MappingNode {
		return
	}
	deleteMappingChild(telemetry, keyConsent)
}
