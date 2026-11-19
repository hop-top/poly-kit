package config

import (
	"gopkg.in/yaml.v3"
)

// deepMap is the dst wrapper Load uses when Options.DeepMergeMaps is set and
// dst is a *map[string]any. Every layer decodes through it, so project
// files, overrides and defaults share one merge rule.
type deepMap struct{ m *map[string]any }

// UnmarshalYAML merges the node into the map: nested mappings merge key by
// key at any depth; scalars, lists and nulls replace.
func (d *deepMap) UnmarshalYAML(n *yaml.Node) error {
	var in map[string]any
	if err := n.Decode(&in); err != nil {
		return err
	}
	if *d.m == nil {
		*d.m = map[string]any{}
	}
	mergeMaps(*d.m, in)
	return nil
}

// MarshalYAML lets code that reads the merged layers back (project rules)
// see the map itself.
func (d *deepMap) MarshalYAML() (any, error) { return *d.m, nil }

func mergeMaps(dst, src map[string]any) {
	for k, v := range src {
		sm, sok := v.(map[string]any)
		dm, dok := dst[k].(map[string]any)
		if sok && dok {
			mergeMaps(dm, sm)
			continue
		}
		dst[k] = v
	}
}

// mergeTarget returns what Load decodes layers into.
func mergeTarget(dst any, opts Options) any {
	if !opts.DeepMergeMaps {
		return dst
	}
	if m, ok := dst.(*map[string]any); ok {
		return &deepMap{m: m}
	}
	return dst
}
