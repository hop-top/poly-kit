package pkl

import (
	"fmt"
	"regexp"
	"strconv"

	"hop.top/kit/go/core/config"
)

// ValidateValue checks a key-value pair against the schema.
// Returns nil if valid, or a typed error from the config package.
func ValidateValue(schema *Schema, key, value string) error {
	if schema == nil {
		return fmt.Errorf("pkl: nil schema")
	}

	field := findField(schema, key)
	if field == nil {
		return &config.ErrUnknownKey{
			Key:       key,
			ValidKeys: allPaths(schema),
		}
	}

	if err := checkType(field, key, value); err != nil {
		return err
	}

	return checkConstraints(field, key, value)
}

func findField(s *Schema, key string) *FieldDef {
	for i := range s.Fields {
		if s.Fields[i].Path == key {
			return &s.Fields[i]
		}
	}
	return nil
}

func allPaths(s *Schema) []string {
	out := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		out = append(out, f.Path)
	}
	return out
}

func checkType(f *FieldDef, key, value string) error {
	switch f.Type {
	case TypeInt:
		if _, err := strconv.Atoi(value); err != nil {
			return &config.ErrTypeMismatch{
				Key: key, Expected: "integer", Got: value,
			}
		}
	case TypeFloat:
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return &config.ErrTypeMismatch{
				Key: key, Expected: "float", Got: value,
			}
		}
	case TypeBool:
		if _, err := strconv.ParseBool(value); err != nil {
			return &config.ErrTypeMismatch{
				Key: key, Expected: "boolean", Got: value,
			}
		}
	case TypeStringEnum:
		for _, v := range f.Enum {
			if v == value {
				return nil
			}
		}
		return &config.ErrConstraintViolation{
			Key:        key,
			Constraint: fmt.Sprintf("enum(%v)", f.Enum),
			Value:      value,
		}
	case TypeString, TypeDuration, TypeStringList:
		// strings always pass type check
	}
	return nil
}

func checkConstraints(f *FieldDef, key, value string) error {
	for _, c := range f.Constraints {
		if err := applyConstraint(f, c, key, value); err != nil {
			return err
		}
	}
	return nil
}

func applyConstraint(
	f *FieldDef, c Constraint, key, value string,
) error {
	switch c.Kind {
	case ConstraintMinLen:
		n, _ := toInt(c.Value)
		if len(value) < n {
			return &config.ErrConstraintViolation{
				Key:        key,
				Constraint: fmt.Sprintf("minLen(%d)", n),
				Value:      value,
			}
		}
	case ConstraintMaxLen:
		n, _ := toInt(c.Value)
		if len(value) > n {
			return &config.ErrConstraintViolation{
				Key:        key,
				Constraint: fmt.Sprintf("maxLen(%d)", n),
				Value:      value,
			}
		}
	case ConstraintMin:
		limit, _ := toFloat(c.Value)
		v, err := strconv.ParseFloat(value, 64)
		if err == nil && v < limit {
			return &config.ErrConstraintViolation{
				Key:        key,
				Constraint: fmt.Sprintf("min(%v)", c.Value),
				Value:      value,
			}
		}
	case ConstraintMax:
		limit, _ := toFloat(c.Value)
		v, err := strconv.ParseFloat(value, 64)
		if err == nil && v > limit {
			return &config.ErrConstraintViolation{
				Key:        key,
				Constraint: fmt.Sprintf("max(%v)", c.Value),
				Value:      value,
			}
		}
	case ConstraintPattern:
		pat, _ := c.Value.(string)
		matched, err := regexp.MatchString(pat, value)
		if err == nil && !matched {
			return &config.ErrConstraintViolation{
				Key:        key,
				Constraint: fmt.Sprintf("pattern(%s)", pat),
				Value:      value,
			}
		}
	case ConstraintBetween:
		lo, hi := betweenBounds(c.Value)
		v, err := strconv.ParseFloat(value, 64)
		if err == nil && (v < lo || v > hi) {
			return &config.ErrConstraintViolation{
				Key:        key,
				Constraint: fmt.Sprintf("between(%v, %v)", lo, hi),
				Value:      value,
			}
		}
	}
	return nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func betweenBounds(v any) (float64, float64) {
	switch b := v.(type) {
	case [2]int:
		return float64(b[0]), float64(b[1])
	case [2]float64:
		return b[0], b[1]
	case [2]any:
		lo, _ := toFloat(b[0])
		hi, _ := toFloat(b[1])
		return lo, hi
	}
	return 0, 0
}
