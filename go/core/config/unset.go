package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Unset removes a config key from the target scope's file.
// Returns ErrKeyNotFound if the key is not present in the target scope.
// Cleans up empty parent mappings after removal.
func Unset(key string, scope Scope, opts Options) error {
	path, err := ScopePath(opts, scope)
	if err != nil {
		return err
	}

	doc, err := parseOrCreateDoc(path)
	if err != nil {
		return fmt.Errorf("config unset %s: %w", path, err)
	}

	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return fmt.Errorf("config unset: %w", ErrKeyNotFound)
		}
		root = root.Content[0]
	}

	parts := strings.Split(key, ".")
	parent := walkToParent(root, parts)
	if parent == nil {
		return fmt.Errorf("config unset: %w", ErrKeyNotFound)
	}

	leaf := parts[len(parts)-1]
	if !removeFromMapping(parent, leaf) {
		return fmt.Errorf("config unset: %w", ErrKeyNotFound)
	}

	cleanEmptyMappings(root, parts[:len(parts)-1])

	if err := writeDoc(path, doc); err != nil {
		return fmt.Errorf("config unset %s: %w", path, err)
	}
	defaultCache.invalidate(path)
	return nil
}

// walkToParent navigates to the parent mapping of the leaf key.
func walkToParent(root *yaml.Node, parts []string) *yaml.Node {
	cur := root
	for _, p := range parts[:len(parts)-1] {
		if cur.Kind != yaml.MappingNode {
			return nil
		}
		found := false
		for i := 0; i < len(cur.Content)-1; i += 2 {
			if cur.Content[i].Value == p {
				cur = cur.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	if cur.Kind != yaml.MappingNode {
		return nil
	}
	return cur
}

// removeFromMapping removes a key-value pair from a mapping node.
// Returns true if found and removed.
func removeFromMapping(mapping *yaml.Node, key string) bool {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(
				mapping.Content[:i],
				mapping.Content[i+2:]...,
			)
			return true
		}
	}
	return false
}

// cleanEmptyMappings walks back up the key path and removes any
// mapping that became empty after a child was deleted.
func cleanEmptyMappings(root *yaml.Node, parentParts []string) {
	if len(parentParts) == 0 {
		return
	}

	// Walk to each ancestor, recording the chain.
	chain := make([]*yaml.Node, 0, len(parentParts)+1)
	chain = append(chain, root)
	cur := root
	for _, p := range parentParts {
		if cur.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i < len(cur.Content)-1; i += 2 {
			if cur.Content[i].Value == p {
				cur = cur.Content[i+1]
				chain = append(chain, cur)
				break
			}
		}
	}

	// Walk back from deepest parent to root, pruning empty mappings.
	for i := len(chain) - 1; i >= 1; i-- {
		node := chain[i]
		if node.Kind != yaml.MappingNode || len(node.Content) != 0 {
			break
		}
		parent := chain[i-1]
		removeFromMapping(parent, parentParts[i-1])
	}
}
