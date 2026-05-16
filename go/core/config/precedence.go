package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Layer is one rung of the precedence ladder. Lookup returns (value, ok)
// — empty strings with ok=true count as "set to empty"; ok=false means
// "absent, try the next layer". Origin tags where the value came from.
type Layer interface {
	Origin() Origin
	Lookup(key string) (string, bool)
}

// FlagLayer wraps a flag-name → value map. Detail shown in Resolved is
// "--<key>" by convention.
type FlagLayer struct {
	Flags map[string]string
	// Changed gates which keys count as flag-supplied. nil = consider all
	// entries in Flags. Mirrors pflag's `f.Changed` semantics so callers
	// can distinguish "default-valued flag" from "user-set flag".
	Changed map[string]bool
}

// Origin implements Layer.
func (l FlagLayer) Origin() Origin { return OriginFlag }

// Lookup implements Layer.
func (l FlagLayer) Lookup(key string) (string, bool) {
	if l.Changed != nil && !l.Changed[key] {
		return "", false
	}
	v, ok := l.Flags[key]
	return v, ok
}

// EnvLayer reads <Prefix>_<UPPER(key)> from the process env, with
// dots/dashes turned into underscores to match common conventions.
type EnvLayer struct {
	Prefix string
}

// Origin implements Layer.
func (l EnvLayer) Origin() Origin { return OriginEnv }

// Lookup implements Layer.
func (l EnvLayer) Lookup(key string) (string, bool) {
	return os.LookupEnv(l.envName(key))
}

// EnvName returns the env var name this layer would read for key.
// Useful for Detail strings in Resolved output.
func (l EnvLayer) EnvName(key string) string { return l.envName(key) }

func (l EnvLayer) envName(key string) string {
	k := strings.ToUpper(key)
	k = strings.ReplaceAll(k, ".", "_")
	k = strings.ReplaceAll(k, "-", "_")
	if l.Prefix == "" {
		return k
	}
	return strings.ToUpper(l.Prefix) + "_" + k
}

// YAMLLayer wraps a top-level YAML map (typically loaded from a project or
// global config file). origin tags whether the values came from project,
// global, or some other layer.
type YAMLLayer struct {
	Data    map[string]any
	OriginV Origin
	Path    string // optional; used as Detail when set
}

// LoadYAMLLayer reads a flat top-level YAML file into a YAMLLayer. Missing
// files return an empty layer (Lookup always returns ok=false), matching
// the "absent file = no values" convention used elsewhere in the package.
func LoadYAMLLayer(path string, origin Origin) (YAMLLayer, error) {
	l := YAMLLayer{Data: map[string]any{}, OriginV: origin, Path: path}
	if path == "" {
		return l, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if err := yaml.Unmarshal(data, &l.Data); err != nil {
		return l, err
	}
	if l.Data == nil {
		l.Data = map[string]any{}
	}
	return l, nil
}

// Origin implements Layer.
func (l YAMLLayer) Origin() Origin { return l.OriginV }

// Lookup implements Layer. Only top-level scalar keys are honored;
// nested maps/sequences are intentionally skipped — process-level config
// is flat by convention, mirroring rlz's `readKeys` behavior.
func (l YAMLLayer) Lookup(key string) (string, bool) {
	v, ok := l.Data[key]
	if !ok {
		return "", false
	}
	switch x := v.(type) {
	case string:
		return x, true
	case nil:
		return "", true
	default:
		return toString(x), true
	}
}

// DefaultLayer is the lowest rung; supplies built-in fallbacks.
type DefaultLayer struct {
	Defaults map[string]string
}

// Origin implements Layer.
func (l DefaultLayer) Origin() Origin { return OriginDefault }

// Lookup implements Layer.
func (l DefaultLayer) Lookup(key string) (string, bool) {
	v, ok := l.Defaults[key]
	return v, ok
}

// ResolveString walks layers in the order given (caller decides ladder
// order; canonical order is flag → env → project → global → default) and
// returns the first match. Empty-string values are treated as a hit when
// the layer reports ok=true.
//
// Layers that report ok=false fall through. If no layer matches, the
// returned Resolved has the zero Origin (Default) and an empty Value;
// callers can also pass an explicit DefaultLayer at the tail to make
// fallback explicit.
func ResolveString(key string, layers ...Layer) Resolved[string] {
	for _, l := range layers {
		if v, ok := l.Lookup(key); ok {
			return Resolved[string]{
				Key:    key,
				Value:  v,
				Origin: l.Origin(),
				Detail: detailFor(l, key),
			}
		}
	}
	return Resolved[string]{Key: key, Origin: OriginDefault}
}

// detailFor produces the Detail string for a Layer/key pair without
// requiring layers to implement extra interfaces.
func detailFor(l Layer, key string) string {
	switch x := l.(type) {
	case FlagLayer:
		return "--" + key
	case EnvLayer:
		return x.EnvName(key)
	case YAMLLayer:
		return x.Path
	case DefaultLayer:
		return "built-in"
	}
	return ""
}

// toString coerces YAML-decoded scalar types to their canonical string
// form. Sequences/maps are rendered via fmt's %v fallback — callers that
// need structured access should query the YAML node directly via Get.
func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", x)
	}
}
