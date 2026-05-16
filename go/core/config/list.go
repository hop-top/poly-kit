package config

import (
	"fmt"
	"sort"
)

// Entry represents a single config key-value pair with its source scope.
type Entry struct {
	Key   string
	Value string
	Scope Scope
}

// List returns all effective config entries across all layers.
// Later scopes shadow earlier ones -- only the effective value is returned.
// Results are sorted by key.
func List(opts Options) ([]Entry, error) {
	type scopedPath struct {
		scope Scope
		path  string
	}

	layers := []scopedPath{
		{ScopeSystem, opts.SystemConfigPath},
		{ScopeUser, opts.UserConfigPath},
		{ScopeProject, opts.ProjectConfigPath},
	}

	merged := make(map[string]Entry)

	for _, sp := range layers {
		if sp.path == "" {
			continue
		}
		doc, err := defaultCache.get(sp.path)
		if err != nil {
			return nil, fmt.Errorf("config list %s: %w", sp.path, err)
		}
		if doc == nil {
			continue
		}
		for _, leaf := range collectLeaves(doc, "") {
			merged[leaf.Key] = Entry{
				Key:   leaf.Key,
				Value: leaf.Value,
				Scope: sp.scope,
			}
		}
	}

	out := make([]Entry, 0, len(merged))
	for _, e := range merged {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out, nil
}
