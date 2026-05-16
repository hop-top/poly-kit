package output

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Formatter encodes a value to an io.Writer in a specific format.
//
// Implementations declare their key, file extensions, and the option keys
// they accept. The registry validates --format-opt input against Options()
// before invoking Render, so Render can trust opts to contain only declared
// keys with values coerced to declared types.
type Formatter interface {
	// Key returns the unique format identifier exposed via --format <key>.
	Key() string

	// Extensions returns file extensions (with leading dot, e.g. ".csv")
	// that map to this formatter for --output extension inference.
	// May return an empty slice when no extension applies.
	Extensions() []string

	// Options returns the option specs accepted by this formatter via
	// --format-opt key=value. Returning nil means the formatter has no
	// options.
	Options() []OptionSpec

	// Render writes data to w. opts contains only validated option values
	// keyed by OptionSpec.Name; missing keys mean the caller did not set
	// them (Render should fall back to spec defaults via opts.GetOr).
	// cols is the user-requested column projection; an empty slice means
	// "all default columns".
	Render(w io.Writer, data any, opts Options, cols []string) error
}

// OptionType identifies the kind of value an OptionSpec accepts.
type OptionType int

const (
	OptString OptionType = iota
	OptInt
	OptBool
	OptEnum
)

// OptionSpec describes one option accepted by a Formatter.
type OptionSpec struct {
	Name    string     // option key, e.g. "delimiter"
	Type    OptionType // value kind
	Default any        // default value (must match Type); zero value if nil
	Usage   string     // short help text for --format-help
	Enum    []string   // allowed values when Type == OptEnum
}

// Options is a validated map of option values produced by parsing
// --format-opt key=value pairs against a Formatter's Options() specs.
type Options map[string]any

// GetString returns the string value for key or "" if absent.
func (o Options) GetString(key string) string {
	v, ok := o[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// GetInt returns the int value for key or 0 if absent.
func (o Options) GetInt(key string) int {
	v, ok := o[key]
	if !ok {
		return 0
	}
	n, _ := v.(int)
	return n
}

// GetBool returns the bool value for key or false if absent.
func (o Options) GetBool(key string) bool {
	v, ok := o[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// GetOr returns the value for key or fallback if absent.
func (o Options) GetOr(key string, fallback any) any {
	if v, ok := o[key]; ok {
		return v
	}
	return fallback
}

// ParseOptions validates raw key=value pairs against specs and returns the
// coerced Options map. Unknown keys, type errors, and out-of-enum values
// produce an error listing the offending key and the valid set.
//
// A pair with no '=' (e.g. "no-header") is treated as bool true; this is
// only valid when the matching spec has Type == OptBool.
//
// Defaults from specs are filled in for any keys not present in pairs.
func ParseOptions(pairs []string, specs []OptionSpec) (Options, error) {
	specByName := make(map[string]OptionSpec, len(specs))
	for _, s := range specs {
		specByName[s.Name] = s
	}

	out := Options{}
	for _, raw := range pairs {
		key, val, hasEq := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("empty option key in %q", raw)
		}
		spec, ok := specByName[key]
		if !ok {
			return nil, fmt.Errorf("unknown option %q (valid: %s)",
				key, strings.Join(specNames(specs), ", "))
		}
		if !hasEq {
			if spec.Type != OptBool {
				return nil, fmt.Errorf("option %q requires a value (e.g. %s=...)", key, key)
			}
			out[key] = true
			continue
		}
		coerced, err := coerce(spec, val)
		if err != nil {
			return nil, err
		}
		out[key] = coerced
	}

	for _, s := range specs {
		if _, ok := out[s.Name]; ok {
			continue
		}
		if s.Default != nil {
			out[s.Name] = s.Default
		}
	}
	return out, nil
}

func coerce(spec OptionSpec, val string) (any, error) {
	switch spec.Type {
	case OptString:
		return val, nil
	case OptInt:
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("option %q: %q is not an int", spec.Name, val)
		}
		return n, nil
	case OptBool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("option %q: %q is not a bool", spec.Name, val)
		}
		return b, nil
	case OptEnum:
		for _, allowed := range spec.Enum {
			if val == allowed {
				return val, nil
			}
		}
		return nil, fmt.Errorf("option %q: %q not in {%s}",
			spec.Name, val, strings.Join(spec.Enum, ", "))
	default:
		return nil, fmt.Errorf("option %q: unknown type", spec.Name)
	}
}

func specNames(specs []OptionSpec) []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	return names
}
