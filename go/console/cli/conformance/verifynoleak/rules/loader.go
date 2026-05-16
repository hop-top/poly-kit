package rules

import (
	"fmt"

	"hop.top/kit/go/conformance/scenariorules"
)

// LoadDefault returns the rule set embedded in the kit binary at
// build time. Delegates to the shared scenariorules loader so the
// canonical wire-format stays in lockstep with verify-stories.
func LoadDefault() (*Set, error) {
	d, err := scenariorules.LoadDefault()
	if err != nil {
		return nil, err
	}
	return fromDocument(d)
}

// LoadFromPath reads a rules JSON file from disk. Used by the
// --rules-file flag and KIT_SCENARIO_RULES_FILE env override.
// Failure is fatal at the call site — operators should not silently
// fall back to embedded rules; that would defeat the override.
func LoadFromPath(path string) (*Set, error) {
	d, err := scenariorules.LoadFromPath(path)
	if err != nil {
		return nil, err
	}
	return fromDocument(d)
}

// fromDocument materializes a leak-detector *Set from the shared
// wire-format *Document. The dispatch on Kind builds a Rule with the
// matcher fields populated; the validity of each Kind has already
// been verified by the shared loader (it refuses unknown kinds before
// we get here).
func fromDocument(d *scenariorules.Document) (*Set, error) {
	set := &Set{
		SchemaVersion: d.SchemaVersion,
		RulesVersion:  d.RulesVersion,
		Verbs:         d.VerbSet(),
		TopLevelKeys:  d.TopLevelKeySet(),
		Rules:         make([]Rule, 0, len(d.CompoundRules)),
	}
	for _, c := range d.CompoundRules {
		r, err := buildRule(c, d.Source)
		if err != nil {
			return nil, err
		}
		set.Rules = append(set.Rules, r)
	}
	return set, nil
}

// buildRule materializes one Rule from the shared wire-format. Kinds
// have already been validated by scenariorules.LoadBytes; per-kind
// invariants too. This function only translates fields to leak's
// matcher-internal Rule struct.
func buildRule(c scenariorules.CompoundRule, source string) (Rule, error) {
	r := Rule{ID: c.ID, Description: c.Description, Kind: RuleKind(c.Kind)}
	switch r.Kind {
	case KindKeyAtRoot:
		r.Key = c.Key
	case KindAnyKeyInSet:
		r.Keys = append([]string(nil), c.Keys...)
	case KindAssertionsListVerbs:
		r.MinCount = c.MinCount
		if r.MinCount <= 0 {
			r.MinCount = 2
		}
	case KindJudgeBlockWithPromptAndScore:
		// no kind-specific config
	default:
		// scenariorules.LoadBytes already rejected unknown kinds;
		// reaching here means the shared loader and this dispatch
		// disagree — surface a clear binary-state error rather than
		// silently dropping the rule.
		return r, fmt.Errorf("rules file %s rule %s: leak detector does not implement kind %q (binary inconsistency)", source, c.ID, c.Kind)
	}
	return r, nil
}
