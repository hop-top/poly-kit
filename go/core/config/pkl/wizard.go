package pkl

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"hop.top/kit/go/console/wizard"
)

// coerceDefault ensures the default value matches the wizard step's
// expected type. TextInput steps require string defaults, but PKL
// parsing returns native int/float for numeric fields.
func coerceDefault(f FieldDef) any {
	if f.Default == nil {
		return nil
	}
	switch f.Type {
	case TypeInt, TypeFloat:
		return fmt.Sprintf("%v", f.Default)
	case TypeBool:
		switch v := f.Default.(type) {
		case string:
			return v == "true"
		default:
			return f.Default
		}
	default:
		return f.Default
	}
}

// WizardSteps converts a PKL schema into wizard steps for
// auto-generated onboarding flows. Computed fields are skipped;
// they resolve at eval time via [Resolve].
func WizardSteps(schema *Schema) ([]wizard.Step, error) {
	if schema == nil {
		return nil, fmt.Errorf("pkl: nil schema")
	}

	var steps []wizard.Step
	for _, f := range schema.Fields {
		if f.Computed {
			continue
		}

		s, err := fieldToStep(f)
		if err != nil {
			return nil, fmt.Errorf("pkl: field %q: %w", f.Path, err)
		}

		s = applyModifiers(s, f)
		steps = append(steps, s)
	}
	return steps, nil
}

// fieldToStep maps a FieldDef type to the appropriate wizard step.
func fieldToStep(f FieldDef) (wizard.Step, error) {
	label := f.Description
	if label == "" {
		label = f.Path
	}

	switch f.Type {
	case TypeString:
		s := wizard.TextInput(f.Path, label)
		if v := buildValidator(f); v != nil {
			s = s.WithValidateText(v)
		}
		return s, nil

	case TypeInt, TypeFloat:
		s := wizard.TextInput(f.Path, label)
		s = s.WithValidateText(numberValidator(f))
		return s, nil

	case TypeBool:
		return wizard.Confirm(f.Path, label), nil

	case TypeStringEnum:
		return wizard.Select(
			f.Path, label, enumToOptions(f.Enum),
		), nil

	case TypeStringList:
		opts := enumToOptions(f.Enum)
		if len(opts) > 0 {
			return wizard.MultiSelect(f.Path, label, opts), nil
		}
		// No predefined options — fall back to text input.
		s := wizard.TextInput(f.Path, label)
		if v := buildValidator(f); v != nil {
			s = s.WithValidateText(v)
		}
		return s, nil

	case TypeDuration:
		s := wizard.TextInput(f.Path, label)
		s = s.WithValidateText(durationValidator())
		return s, nil

	default:
		return wizard.Step{}, fmt.Errorf("unsupported type %d", f.Type)
	}
}

// applyModifiers attaches Required, Default, Group, Description,
// and When conditions to a step.
func applyModifiers(s wizard.Step, f FieldDef) wizard.Step {
	if f.Required {
		s = s.WithRequired()
	}
	if f.Default != nil {
		s = s.WithDefault(coerceDefault(f))
	}
	if f.Group != "" {
		s = s.WithGroup(f.Group)
	}
	if f.Description != "" {
		s = s.WithDescription(f.Description)
	}
	if f.When != nil && len(f.When.DependsOn) > 0 {
		s = s.WithWhen(
			f.When.DependsOn[0],
			buildWhenPredicate(f.When),
		)
	}
	return s
}

// buildValidator creates a composite text validator from field
// constraints. Returns nil when no constraints apply.
func buildValidator(f FieldDef) func(string) error {
	var fns []func(string) error
	for _, c := range f.Constraints {
		switch c.Kind {
		case ConstraintMinLen:
			n, _ := toInt(c.Value)
			fns = append(fns, func(s string) error {
				if len(s) < n {
					return fmt.Errorf("minimum length %d", n)
				}
				return nil
			})
		case ConstraintMaxLen:
			n, _ := toInt(c.Value)
			fns = append(fns, func(s string) error {
				if len(s) > n {
					return fmt.Errorf("maximum length %d", n)
				}
				return nil
			})
		case ConstraintPattern:
			pat, _ := c.Value.(string)
			re := regexp.MustCompile(pat)
			fns = append(fns, func(s string) error {
				if !re.MatchString(s) {
					return fmt.Errorf("must match %s", pat)
				}
				return nil
			})
		case ConstraintBetween:
			loF, hiF := betweenBounds(c.Value)
			lo, hi := int(loF), int(hiF)
			fns = append(fns, func(s string) error {
				n, err := strconv.Atoi(s)
				if err != nil {
					return fmt.Errorf("expected integer")
				}
				if n < lo || n > hi {
					return fmt.Errorf("must be between %d and %d", lo, hi)
				}
				return nil
			})
		}
	}
	if len(fns) == 0 {
		return nil
	}
	return func(s string) error {
		for _, fn := range fns {
			if err := fn(s); err != nil {
				return err
			}
		}
		return nil
	}
}

// numberValidator returns a validator that ensures the input parses
// as an int or float depending on field type, applying min/max/between.
func numberValidator(f FieldDef) func(string) error {
	isInt := f.Type == TypeInt
	return func(s string) error {
		if isInt {
			v, err := strconv.Atoi(s)
			if err != nil {
				return fmt.Errorf("expected integer")
			}
			for _, c := range f.Constraints {
				switch c.Kind {
				case ConstraintMin:
					n, _ := toInt(c.Value)
					if v < n {
						return fmt.Errorf("minimum %d", n)
					}
				case ConstraintMax:
					n, _ := toInt(c.Value)
					if v > n {
						return fmt.Errorf("maximum %d", n)
					}
				case ConstraintBetween:
					loF, hiF := betweenBounds(c.Value)
					lo, hi := int(loF), int(hiF)
					if v < lo || v > hi {
						return fmt.Errorf(
							"must be between %d and %d", lo, hi,
						)
					}
				}
			}
			return nil
		}
		// float
		_, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fmt.Errorf("expected number")
		}
		return nil
	}
}

// durationValidator returns a validator that checks single
// number+unit duration strings (e.g. "5s", "1.5h").
func durationValidator() func(string) error {
	// Accept common PKL/Go duration patterns.
	re := regexp.MustCompile(
		`^(\d+(\.\d+)?)(ns|us|µs|ms|s|min|h|d)$`,
	)
	return func(s string) error {
		if !re.MatchString(s) {
			return fmt.Errorf("invalid duration %q", s)
		}
		return nil
	}
}

// buildWhenPredicate converts a FieldCondition expression into a
// predicate function for wizard conditional visibility.
// Parses full "field operator value" expressions (e.g. `lang != "go"`).
// The DependsOn[0] field is used by wizard's WithWhen to resolve which
// key to check; the predicate only evaluates the comparison.
func buildWhenPredicate(cond *FieldCondition) func(any) bool {
	expr := strings.TrimSpace(cond.Expression)

	// Parse "field != value" or standalone "!= value".
	if parts := strings.SplitN(expr, " != ", 2); len(parts) == 2 {
		target := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		if target == "true" {
			return func(v any) bool { b, _ := v.(bool); return !b }
		}
		if target == "false" {
			return func(v any) bool { b, _ := v.(bool); return b }
		}
		return func(v any) bool { s, _ := v.(string); return s != target }
	}

	// Parse "field == value" or standalone "== value".
	if parts := strings.SplitN(expr, " == ", 2); len(parts) == 2 {
		target := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		if target == "true" {
			return func(v any) bool { b, _ := v.(bool); return b }
		}
		if target == "false" {
			return func(v any) bool { b, _ := v.(bool); return !b }
		}
		return func(v any) bool { s, _ := v.(string); return s == target }
	}

	// Bare field name: treat as truthy check.
	return func(v any) bool {
		if b, ok := v.(bool); ok {
			return b
		}
		if s, ok := v.(string); ok {
			return s != ""
		}
		return v != nil
	}
}

// enumToOptions converts string values to wizard options.
func enumToOptions(values []string) []wizard.Option {
	opts := make([]wizard.Option, len(values))
	for i, s := range values {
		opts[i] = wizard.Option{Value: s, Label: s}
	}
	return opts
}
