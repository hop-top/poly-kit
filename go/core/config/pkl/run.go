package pkl

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"hop.top/kit/go/console/wizard"
	"hop.top/kit/go/core/config"
)

// WizardOpts configures RunWizard behavior.
type WizardOpts struct {
	ConfigOpts config.Options
	Scope      config.Scope
	DryRun     bool
	Headless   map[string]any     // pre-filled answers for CI
	WizardOpts []wizard.RunOption // pass-through to wizard.Run
}

// RunWizard loads a PKL schema, generates wizard steps, collects
// answers, resolves computed defaults, validates, and writes config.
func RunWizard(
	ctx context.Context, pklPath string, opts WizardOpts,
) error {
	schema, err := LoadSchema(pklPath)
	if err != nil {
		return fmt.Errorf("pkl: load schema: %w", err)
	}

	fields := prefillDefaults(schema.Fields, opts.ConfigOpts)
	modifiedSchema := &Schema{
		ModuleName: schema.ModuleName,
		Fields:     fields,
	}

	steps, err := WizardSteps(modifiedSchema)
	if err != nil {
		return fmt.Errorf("pkl: wizard steps: %w", err)
	}

	w, err := wizard.New(steps...)
	if err != nil {
		return fmt.Errorf("pkl: create wizard: %w", err)
	}

	w.SetOnComplete(func(results map[string]any) error {
		return writeConfig(ctx, pklPath, schema, results, opts)
	})

	if opts.DryRun {
		w.SetDryRun(true)
	}

	if opts.Headless != nil {
		_, err = wizard.RunHeadless(ctx, w, opts.Headless)
		return err
	}

	return wizard.Run(ctx, w, opts.WizardOpts...)
}

// validationForms returns the string forms that must be fed through
// [ValidateValue] for a single answered field.
//
// ValidateValue's contract is string-in: checkType parses the string as
// int/float/bool, and the constraint checks (minLen, pattern, min/max)
// all operate on the string. Passing the typed value would mean
// duplicating that logic in a second, drifting implementation, so the
// stringification is kept — but confined to validation only.
//
// For TypeStringList the old code validated the whole slice as one
// string, which under %v was the meaningless "[a b]". Length and
// pattern constraints were therefore being applied to Go's slice
// debug-print rather than to the elements. Each element is validated
// separately instead, so a maxLen or pattern constraint on a list field
// now constrains the individual entries.
//
// This changes validation semantics in both directions, because the
// bracketed debug-print is neither a subset nor a superset of the
// elements it wraps:
//
//   - Fixes false negatives: an over-long or non-matching element used
//     to slip through whenever "[a b]" happened to satisfy the
//     constraint, and is now rejected.
//   - Fixes false positives: constraints sized for real entries used to
//     be measured against the brackets and spaces too. maxLen(5) on the
//     two-element list {"a","b"} saw the 5-char "[a b]" plus every
//     added element; pattern("^[a-z]+$") could never match any list at
//     all. Both now apply per element and pass.
func validationForms(field FieldDef, val any) []string {
	if field.Type != TypeStringList {
		return []string{fmt.Sprintf("%v", val)}
	}

	elems, ok := toStringSlice(val)
	if !ok {
		// Not list-shaped (e.g. a free-text answer for a list field
		// with no predefined options). Validate as a plain scalar.
		return []string{fmt.Sprintf("%v", val)}
	}

	return elems
}

// writeValue converts an answered value into the Go value handed to
// [config.SetValue], which derives the YAML tag from the Go type.
//
// Values reach here by two routes with different Go types, and both
// must be normalised. [Resolve] round-trips answers through JSON, so
// numbers arrive as float64 and lists as []any. When the pkl binary is
// unavailable or evaluation fails, writeConfig falls back to the raw
// wizard answers instead — and Int/Float fields render as TextInput
// steps, whose answers are strings. Handling only the evaluator's
// shapes would leave the degraded path writing `retries: "7"`.
//
//   - TypeInt: float64 is narrowed to int and a numeric string parsed,
//     yielding `retries: 3` rather than `retries: 3.0` or "3".
//   - TypeFloat: float64 kept, numeric string parsed: `ratio: 0.5`.
//   - TypeBool: bool kept, "true"/"false" parsed: `enabled: true`.
//   - TypeStringList: normalised to []string so yaml emits a real
//     sequence. Previously %v collapsed it to the literal "[a b]",
//     which is not a YAML sequence and cannot be parsed back into a
//     list — the value was destroyed, not merely mistyped. A raw
//     wizard answer is a comma-separated string and is split the same
//     way; see [toStringSlice] for why that is the usual shape.
//   - TypeDuration: written as a string. PKL's JsonRenderer cannot
//     render a Duration at all ("Cannot render value of type
//     `Duration` as JSON"), so a duration never survives [Resolve];
//     it only ever reaches here as the raw wizard answer, which is
//     already a string like "5s". A string also round-trips cleanly
//     through time.ParseDuration on the consumer side, whereas an
//     integer nanosecond count would not match a `Duration` schema
//     field.
//   - TypeStringEnum: written as a string. The enum members are
//     declared as string literals in the PKL union type and
//     ValidateValue compares them as strings, so the YAML must stay a
//     string for the value to still match its schema.
func writeValue(field FieldDef, val any) any {
	switch field.Type {
	case TypeInt:
		if f, ok := toFloat(val); ok {
			return int64(f)
		}
		// Wizard TextInput answers arrive as strings. ValidateValue has
		// already accepted this as an integer, so ParseInt cannot fail.
		if s, ok := val.(string); ok {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n
			}
		}
		return val

	case TypeFloat:
		if f, ok := toFloat(val); ok {
			return f
		}
		if s, ok := val.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f
			}
		}
		return val

	case TypeBool:
		if b, ok := val.(bool); ok {
			return b
		}
		if s, ok := val.(string); ok {
			if b, err := strconv.ParseBool(s); err == nil {
				return b
			}
		}
		return val

	case TypeStringList:
		if elems, ok := toStringSlice(val); ok {
			return elems
		}
		return val

	case TypeDuration, TypeStringEnum:
		if s, ok := val.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", val)

	default:
		// TypeString: already a string.
		return val
	}
}

// toStringSlice normalises the list shapes the wizard and the PKL
// evaluator can produce. Resolve decodes JSON into map[string]any, so
// list answers arrive as []any; the wizard's MultiSelect step yields
// []string directly.
//
// A plain string is also list-shaped here, and this is the common case
// rather than an exotic one. parseField never populates Enum for a
// Listing<T>, so fieldToStep's MultiSelect branch is unreachable and
// every TypeStringList field renders as a TextInput whose answer is a
// string. When Resolve succeeds the evaluator hands back a real []any
// and the string never surfaces, but on the degraded path writeConfig
// falls back to those raw answers — and without this case a list field
// wrote the scalar `tags: alpha,beta` instead of a YAML sequence.
//
// The separator is a comma, with surrounding space trimmed and empty
// entries dropped. That is not a new convention: it is what
// wizard.parseChoices already accepts at the MultiSelect prompt and
// what cli.splitAndTrim applies to set-style flags, so a list typed
// into the wizard reads the same whether or not the pkl binary is
// present. An empty or all-blank string yields an empty list, not a
// one-element list containing "".
func toStringSlice(val any) ([]string, bool) {
	switch v := val.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	case string:
		return splitList(v), true
	}
	return nil, false
}

// splitList parses a comma-separated list answer into its elements.
func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// prefillDefaults reads existing config values and overrides schema
// defaults so the wizard shows current values, not schema defaults.
func prefillDefaults(
	fields []FieldDef, cfgOpts config.Options,
) []FieldDef {
	out := make([]FieldDef, len(fields))
	copy(out, fields)

	for i := range out {
		val, err := config.Get(out[i].Path, cfgOpts)
		if err != nil {
			continue // keep schema default
		}
		out[i].Default = val
	}
	return out
}

// writeConfig resolves computed fields, validates, and writes each
// key to the config file.
func writeConfig(
	ctx context.Context,
	pklPath string,
	schema *Schema,
	results map[string]any,
	opts WizardOpts,
) error {
	resolved, err := Resolve(ctx, pklPath, results)
	resolveFailed := err != nil
	if resolveFailed {
		log.Printf(
			"pkl: resolve computed fields: %v (continuing with raw answers)",
			err,
		)
		resolved = results
	}

	for _, field := range schema.Fields {
		val, ok := resolved[field.Path]
		if !ok {
			val, ok = results[field.Path]
		}
		if !ok {
			continue // skipped by When condition
		}

		// If resolve failed and field is computed, skip it — we can't
		// produce a value without the pkl binary.
		if field.Computed && resolveFailed {
			continue
		}

		// ValidateValue is string-based by contract. Stringify only for
		// the validation call; the write below gets the typed value so
		// numbers, bools and lists keep their YAML types.
		for _, strVal := range validationForms(field, val) {
			if err := ValidateValue(schema, field.Path, strVal); err != nil {
				return fmt.Errorf(
					"pkl: validate %s=%q: %w", field.Path, strVal, err,
				)
			}
		}

		if err := config.SetValue(
			field.Path, writeValue(field, val), opts.Scope, opts.ConfigOpts,
		); err != nil {
			return fmt.Errorf(
				"pkl: write %s: %w", field.Path, err,
			)
		}
	}
	return nil
}
