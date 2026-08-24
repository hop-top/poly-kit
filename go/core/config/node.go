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

// Resolved YAML tags that scalarToValue converts to a non-string Go
// type. Every other tag -- including !!binary, !!timestamp, and any
// custom or unrecognized tag -- yields the scalar's raw source text, so
// Get's return type stays within the documented set.
const (
	intTag   = "!!int"
	floatTag = "!!float"
	boolTag  = "!!bool"
	nullTag  = "!!null"
)

// nodeToValue converts a yaml.Node to a Go value, preserving the type
// implied by each scalar's resolved YAML tag: !!int → int, !!float →
// float64, !!bool → bool, !!null → nil, everything else → string.
// Sequences become []any so element types survive; mappings become
// map[string]any.
func nodeToValue(n *yaml.Node) any {
	switch n.Kind {
	case yaml.ScalarNode:
		return scalarToValue(n)
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, nodeToValue(c))
		}
		return out
	case yaml.MappingNode:
		// Decoded key-by-key rather than via a single Decode call:
		// decoding a whole mapping yields map[any]any when any key is
		// non-string, which would break the map[string]any contract.
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i < len(n.Content)-1; i += 2 {
			out[n.Content[i].Value] = nodeToValue(n.Content[i+1])
		}
		return out
	}
	return nil
}

// scalarToValue resolves a scalar node to its tagged Go type.
//
// Only the four tags above are converted; anything else returns the raw
// source text. yaml.v3 does the conversion for the tags in the
// whitelist so spellings it accepts (hex, octal and underscored ints,
// .inf/.nan floats, YAML 1.2 core schema booleans) resolve exactly as
// Load would resolve them for the same file.
//
// Tags outside the whitelist are deliberately not decoded:
//
//   - !!binary would come back as the *decoded* bytes in a string, so a
//     caller reading a base64 field would silently receive different
//     text than the file holds, with no way to detect it. Invalid
//     base64 decodes to "" with no error, losing the value entirely.
//   - !!timestamp would come back as a time.Time, making Get's return
//     type depend on whether a value happens to parse as a date.
//   - Custom tags (!mytype) carry no meaning to this package.
//
// Config values are opaque tokens, so raw text is the honest answer for
// all of them.
func scalarToValue(n *yaml.Node) any {
	switch n.Tag {
	case intTag, floatTag, boolTag, nullTag:
	default:
		return n.Value
	}

	var v any
	if err := n.Decode(&v); err != nil {
		// Explicitly tagged but unconvertible (e.g. `!!int abc`): fall
		// back to the raw text rather than dropping the value.
		return n.Value
	}

	// An !!int too large for int decodes to uint64. Return the raw text
	// rather than a width the contract does not name.
	switch v.(type) {
	case int, float64, bool, nil:
		return v
	default:
		return n.Value
	}
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
