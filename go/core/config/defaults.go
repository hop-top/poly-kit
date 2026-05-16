package config

import (
	"fmt"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// applyDefaults seeds dst with values from defaults, then file/env/CLI layers
// merge over the top. defaults must be a pointer to a struct of the same type
// as dst (or a compatible shape — anything yaml will unmarshal into dst from
// defaults' YAML representation).
//
// The implementation round-trips through YAML so the merge semantics match
// every other layer in Load: per-leaf overlay, slices replaced, maps merged
// recursively. This keeps "defaults" indistinguishable from "an extra file
// loaded first" for the caller.
func applyDefaults(dst, defaults any) error {
	if defaults == nil {
		return nil
	}
	data, err := yaml.Marshal(defaults)
	if err != nil {
		return fmt.Errorf("marshal defaults: %w", err)
	}
	if err := yaml.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("apply defaults: %w", err)
	}
	return nil
}

// SeedDefaults walks defaults (a pointer to a struct) and registers each leaf
// with viper.SetDefault, using the yaml tag (or lowercased field name) as the
// dotted key. This keeps typed defaults colocated with the Config struct
// while still letting viper-using code see them via viper.GetString and
// friends.
//
// Nested structs become dotted keys: a field tagged "auth" containing a
// field tagged "token" sets the viper key "auth.token". Slice and map
// values are stored under the parent key as a single value (not deep-walked).
//
// SeedDefaults is independent of Load; callers can use it to seed viper
// before any file work, or skip it and seed viper themselves. Load also
// invokes the equivalent path automatically when Options.Defaults and
// Options.Viper are both set.
func SeedDefaults(v *viper.Viper, defaults any) error {
	if v == nil {
		return fmt.Errorf("viper is nil")
	}
	if defaults == nil {
		return nil
	}
	flat, err := flattenForViper(defaults)
	if err != nil {
		return fmt.Errorf("seed defaults: %w", err)
	}
	for k, val := range flat {
		v.SetDefault(k, val)
	}
	return nil
}

// flattenForViper turns a struct (or map) into a flat dotted-key map suitable
// for viper.SetDefault calls. The same yaml round-trip used by applyDefaults
// gives us the same field-name resolution as the rest of the loader.
func flattenForViper(src any) (map[string]any, error) {
	data, err := yaml.Marshal(src)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	flat := map[string]any{}
	flattenInto(flat, "", raw)
	return flat, nil
}

func flattenInto(flat map[string]any, prefix string, v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flattenInto(flat, key, child)
		}
	case map[any]any:
		// yaml.v3 sometimes returns map[any]any for unknown shapes; normalise.
		for k, child := range t {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			key := ks
			if prefix != "" {
				key = prefix + "." + ks
			}
			flattenInto(flat, key, child)
		}
	default:
		// Leaves (scalars, slices) get stored under the current prefix.
		if prefix == "" {
			return
		}
		flat[prefix] = v
	}
}

// syncToViper writes the merged dst back into the given viper instance after
// Load has applied every layer. Callers who use viper.GetString in code paths
// that don't see the typed dst rely on this; pure typed-dst callers can skip
// it (Options.Viper unset).
func syncToViper(v *viper.Viper, src any) error {
	if v == nil || src == nil {
		return nil
	}
	flat, err := flattenForViper(src)
	if err != nil {
		return fmt.Errorf("sync to viper: %w", err)
	}
	for k, val := range flat {
		v.Set(k, val)
	}
	return nil
}
