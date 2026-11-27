package config

import (
	"errors"
	"fmt"
)

// ErrKeyNotFound is returned when a key is not present in any layer.
var ErrKeyNotFound = errors.New("config: key not found")

// Get retrieves a config value by dotted key path, merging across layers.
// Layer precedence: project > user > system.
// Returns the value from the highest-priority layer that contains the key.
//
// The returned value keeps the type implied by the YAML scalar tag, and
// is always one of int, float64, bool, nil or string. Only !!int,
// !!float, !!bool and !!null are converted; every other tag -- !!str,
// !!binary, !!timestamp, and any custom tag -- yields the scalar's raw
// source text. So a date stays a string rather than becoming a
// time.Time, base64 stays base64 rather than being decoded, and an
// !!int too large for int stays its literal digits.
//
// Sequences come back as []any and mappings as map[string]any, with the
// same rules applied to each element.
func Get(key string, opts Options) (any, error) {
	paths, err := layerPaths(opts)
	if err != nil {
		return nil, fmt.Errorf("config get: %w", err)
	}

	var result any
	found := false

	for _, path := range paths {
		if path == "" {
			continue
		}
		doc, err := defaultCache.get(path)
		if err != nil {
			return nil, fmt.Errorf("config get %s: %w", path, err)
		}
		if doc == nil {
			continue
		}
		node := walkPath(doc, key)
		if node != nil {
			result = nodeToValue(node)
			found = true
		}
	}

	if !found {
		return nil, ErrKeyNotFound
	}
	return result, nil
}

// layerPaths returns file paths in precedence order (lowest to highest).
// The user slot is the effective user file (see effectiveUserPath).
func layerPaths(opts Options) ([]string, error) {
	user, _, err := effectiveUserPath(opts)
	if err != nil {
		return nil, err
	}
	return []string{opts.SystemConfigPath, user, opts.ProjectConfigPath}, nil
}
