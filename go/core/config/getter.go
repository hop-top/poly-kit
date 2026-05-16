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
func Get(key string, opts Options) (any, error) {
	paths := layerPaths(opts)

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
func layerPaths(opts Options) []string {
	return []string{opts.SystemConfigPath, opts.UserConfigPath, opts.ProjectConfigPath}
}
