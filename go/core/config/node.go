package config

import (
	"errors"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// nodeCache holds parsed yaml.Node documents keyed by file path.
type nodeCache struct {
	mu    sync.Mutex
	nodes map[string]*yaml.Node
}

func newNodeCache() *nodeCache {
	return &nodeCache{nodes: make(map[string]*yaml.Node)}
}

// get returns the parsed yaml.Node for a file, caching the result.
// Returns nil, nil for non-existent files.
func (c *nodeCache) get(path string) (*yaml.Node, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if n, ok := c.nodes[path]; ok {
		return n, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	c.nodes[path] = &doc
	return &doc, nil
}

// invalidate removes a path from the cache (called after Set/Unset writes).
func (c *nodeCache) invalidate(path string) {
	c.mu.Lock()
	delete(c.nodes, path)
	c.mu.Unlock()
}

// defaultCache is the package-level node cache.
var defaultCache = newNodeCache()

// walkPath navigates a yaml.Node document by dotted key path.
// Returns the leaf node or nil if not found.
func walkPath(doc *yaml.Node, key string) *yaml.Node {
	if doc == nil {
		return nil
	}

	// Unwrap document node.
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil
		}
		root = root.Content[0]
	}

	parts := strings.Split(key, ".")
	cur := root

	for _, p := range parts {
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
	return cur
}

// walkOrCreate navigates a yaml.Node document by dotted key path,
// creating intermediate mapping nodes as needed.
// Returns the parent mapping node and the key name of the leaf.
func walkOrCreate(doc *yaml.Node, key string) (*yaml.Node, string) {
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			root.Content = append(root.Content, m)
		}
		root = root.Content[0]
	}

	parts := strings.Split(key, ".")
	cur := root

	for _, p := range parts[:len(parts)-1] {
		var next *yaml.Node
		for i := 0; i < len(cur.Content)-1; i += 2 {
			if cur.Content[i].Value == p {
				next = cur.Content[i+1]
				break
			}
		}
		if next == nil {
			kn := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p}
			vn := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			cur.Content = append(cur.Content, kn, vn)
			next = vn
		}
		// If the existing value is not a mapping, replace it.
		if next.Kind != yaml.MappingNode {
			next.Kind = yaml.MappingNode
			next.Tag = "!!map"
			next.Value = ""
			next.Content = nil
		}
		cur = next
	}
	return cur, parts[len(parts)-1]
}

// nodeToValue converts a yaml.Node to a Go value:
// scalar → string, sequence → []string, mapping → map[string]any.
func nodeToValue(n *yaml.Node) any {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value
	case yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
		return out
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i < len(n.Content)-1; i += 2 {
			out[n.Content[i].Value] = nodeToValue(n.Content[i+1])
		}
		return out
	}
	return nil
}

// leafEntry represents a single leaf in a flattened YAML tree.
type leafEntry struct {
	Key   string
	Value string
}

// collectLeaves walks a yaml.Node tree and collects all leaf
// key paths as dotted strings.
func collectLeaves(doc *yaml.Node, prefix string) []leafEntry {
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil
		}
		root = root.Content[0]
	}
	return collectMapping(root, prefix)
}

func collectMapping(n *yaml.Node, prefix string) []leafEntry {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	var out []leafEntry
	for i := 0; i < len(n.Content)-1; i += 2 {
		k := n.Content[i].Value
		v := n.Content[i+1]
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		if v.Kind == yaml.MappingNode {
			out = append(out, collectMapping(v, full)...)
		} else {
			out = append(out, leafEntry{Key: full, Value: v.Value})
		}
	}
	return out
}
