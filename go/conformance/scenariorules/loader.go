package scenariorules

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Embedded canonical rules. Updates land via PRs to
// contracts/scenario-rules.json and ship in the next kit binary.
//
//go:embed scenario_rules_embedded.json
var embeddedRules []byte

// SchemaVersionV1 is the only schema_version this loader recognizes.
// A bump to "2" requires a new kit binary; the loader refuses unknown
// versions so a stale binary cannot silently false-negative on rules
// it does not understand.
const SchemaVersionV1 = "1"

// Recognized rule kinds. Loaders accept these; unknown kinds in the
// JSON wire format cause LoadFromPath / LoadBytes / LoadDefault to
// fail loudly. Adding a new kind requires updating both the leak
// detector (which dispatches on Kind) and this constant set.
const (
	KindKeyAtRoot                    = "key_at_root"
	KindAnyKeyInSet                  = "any_key_in_set"
	KindAssertionsListVerbs          = "assertions_list_verbs"
	KindJudgeBlockWithPromptAndScore = "judge_block_with_prompt_and_score"
)

// ErrUnknownRuleKind is wrapped into the loader's error when the
// JSON wire format declares a kind this binary does not implement.
// Callers can errors.Is to surface a precise upgrade nudge.
var ErrUnknownRuleKind = errors.New("unknown rule kind")

// Document is the parsed wire-format. The leak detector consumes it
// to build its matcher tree; the story validator consumes Verbs +
// TopLevelKeys (and the hardcoded "cassette_must_*" pair) to build
// its metadata-key denylist.
type Document struct {
	// Source identifies where the bytes came from ("<embedded>",
	// a file path, or "<bytes>"). Used by error messages.
	Source string

	// SchemaVersion is the wire-format version. Always "1" in v1.
	SchemaVersion string

	// RulesVersion is the calendar-versioned rules timestamp
	// (e.g. "2026.05.12"). Free-form string; consumed by output
	// formatters that want to surface "which rules ran".
	RulesVersion string

	// Verbs is the closed verb set kit treats as
	// scenario-rubric-assertion-kind vocabulary. Used by leak R2
	// and by the story validator's metadata denylist.
	Verbs []string

	// TopLevelKeys is the closed set of top-level keys kit
	// considers scenario-shaped. Used by the story validator's
	// metadata denylist; the leak detector consumes its individual
	// keys via the compound rules.
	TopLevelKeys []string

	// CompoundRules is the kit-side ordered list of detection
	// rules. Each entry is the raw wire-format struct; the leak
	// detector dispatches on Kind to its matcher implementation.
	CompoundRules []CompoundRule
}

// CompoundRule is the wire-format for one entry in compound_rules[].
// Only the field corresponding to Kind is consulted by consumers; the
// loader validates that the required field is present per Kind.
type CompoundRule struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`
	Key         string   `json:"key,omitempty"`
	Keys        []string `json:"keys,omitempty"`
	MinCount    int      `json:"min_count,omitempty"`
}

// rulesFile is the on-disk JSON shape. Internal; callers consume the
// validated *Document. Kept private so adding optional wire-format
// fields stays a non-breaking change at the consumer surface.
type rulesFile struct {
	SchemaVersion string         `json:"schema_version"`
	RulesVersion  string         `json:"rules_version"`
	Verbs         []string       `json:"verbs"`
	TopLevelKeys  []string       `json:"top_level_keys"`
	CompoundRules []CompoundRule `json:"compound_rules"`
}

// LoadDefault returns the rule document embedded in the kit binary
// at build time. This is the path used by every scan / story
// validation unless the operator passed --rules-file (handled by
// LoadFromPath).
func LoadDefault() (*Document, error) {
	return LoadBytes(embeddedRules, "<embedded>")
}

// LoadFromPath reads a rules JSON file from disk. Used by adopter
// override flags (--rules-file) and adopter-lab experiments with
// extended verb sets. Failure is fatal at the call site — operators
// should not silently fall back to embedded rules; that would defeat
// the override.
func LoadFromPath(path string) (*Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open rules file %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read rules file %s: %w", path, err)
	}
	return LoadBytes(data, path)
}

// LoadBytes parses + validates a rules JSON blob. source is the
// human-readable label used in error messages ("<embedded>", a file
// path, or "<bytes>").
func LoadBytes(data []byte, source string) (*Document, error) {
	if source == "" {
		source = "<bytes>"
	}
	var f rulesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse rules file %s: %w", source, err)
	}
	if f.SchemaVersion == "" {
		return nil, fmt.Errorf("rules file %s missing schema_version", source)
	}
	if f.SchemaVersion != SchemaVersionV1 {
		return nil, fmt.Errorf("rules file %s schema_version %q is not supported by this kit binary (expected %q); upgrade kit", source, f.SchemaVersion, SchemaVersionV1)
	}
	for _, c := range f.CompoundRules {
		if err := validateCompound(c, source); err != nil {
			return nil, err
		}
	}
	return &Document{
		Source:        source,
		SchemaVersion: f.SchemaVersion,
		RulesVersion:  f.RulesVersion,
		Verbs:         append([]string(nil), f.Verbs...),
		TopLevelKeys:  append([]string(nil), f.TopLevelKeys...),
		CompoundRules: append([]CompoundRule(nil), f.CompoundRules...),
	}, nil
}

// validateCompound enforces the per-Kind required-field invariants.
// Unknown kinds wrap ErrUnknownRuleKind so consumers can surface a
// precise upgrade nudge.
func validateCompound(c CompoundRule, source string) error {
	switch c.Kind {
	case KindKeyAtRoot:
		if c.Key == "" {
			return fmt.Errorf("rules file %s rule %s: kind %q requires non-empty \"key\"", source, c.ID, c.Kind)
		}
	case KindAnyKeyInSet:
		if len(c.Keys) == 0 {
			return fmt.Errorf("rules file %s rule %s: kind %q requires non-empty \"keys\"", source, c.ID, c.Kind)
		}
	case KindAssertionsListVerbs:
		// MinCount has a sensible default in consumers (2); no
		// requirement here.
	case KindJudgeBlockWithPromptAndScore:
		// No kind-specific configuration.
	default:
		return fmt.Errorf("rules file %s rule %s: %w %q (binary too old? upgrade kit)", source, c.ID, ErrUnknownRuleKind, c.Kind)
	}
	return nil
}

// VerbSet returns the parsed verbs as a set keyed for O(1) lookup.
// Convenience helper for consumers (denylist construction, matcher
// dispatch).
func (d *Document) VerbSet() map[string]struct{} {
	if d == nil {
		return nil
	}
	m := make(map[string]struct{}, len(d.Verbs))
	for _, v := range d.Verbs {
		m[v] = struct{}{}
	}
	return m
}

// TopLevelKeySet returns the parsed top-level keys as a set keyed
// for O(1) lookup.
func (d *Document) TopLevelKeySet() map[string]struct{} {
	if d == nil {
		return nil
	}
	m := make(map[string]struct{}, len(d.TopLevelKeys))
	for _, k := range d.TopLevelKeys {
		m[k] = struct{}{}
	}
	return m
}
