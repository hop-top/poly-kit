package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Set writes a config value at the given dotted key path in the
// target scope's file. Creates the file and parent directories
// if they don't exist. Preserves existing YAML content and comments.
func Set(key, value string, scope Scope, opts Options) error {
	path, err := ScopePath(opts, scope)
	if err != nil {
		return err
	}

	doc, err := parseOrCreateDoc(path)
	if err != nil {
		return fmt.Errorf("config set: %w", err)
	}

	parent, leafKey := walkOrCreate(doc, key)
	setNodeInMapping(parent, leafKey, &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: value,
	})

	if err := writeDoc(path, doc); err != nil {
		return fmt.Errorf("config set: %w", err)
	}

	defaultCache.invalidate(path)
	return nil
}

// SetValue writes a typed config value at the given dotted key path in
// the target scope's file. Unlike [Set], which always writes a
// !!str-tagged scalar (so 0.9 round-trips as the string "0.9"), SetValue
// derives the YAML tag from the Go type of value via yaml.Node.Encode:
// float64(0.9) is written as `key: 0.9`, true as `key: true`, nil as
// `key: null`.
//
// Non-scalar values (maps, slices, structs) are supported: Encode yields
// a mapping or sequence node and it is spliced in whole, replacing any
// previous value at that key. Supporting them costs nothing — the node is
// grafted into the tree exactly like a scalar — and rejecting them would
// force callers to hand-build yaml.Node trees for the entirely reasonable
// case of setting a nested config subtree in one call.
//
// Values yaml cannot marshal at all (funcs, channels) return an error and
// leave the file untouched; they do not panic.
//
// Note that Encode also quotes YAML 1.1 lookalikes such as "yes"/"off",
// which the plain [Set] path emits bare — see TestYAML11Lookalikes.
//
// Creates the file and parent directories if they don't exist. Preserves
// existing YAML content and comments, including any comments attached to
// the key being overwritten.
func SetValue(key string, value any, scope Scope, opts Options) error {
	path, err := ScopePath(opts, scope)
	if err != nil {
		return err
	}

	valueNode, err := encodeValueNode(value)
	if err != nil {
		return fmt.Errorf("config set: encode value for %q: %w", key, err)
	}

	doc, err := parseOrCreateDoc(path)
	if err != nil {
		return fmt.Errorf("config set: %w", err)
	}

	parent, leafKey := walkOrCreate(doc, key)
	setNodeInMapping(parent, leafKey, valueNode)

	if err := writeDoc(path, doc); err != nil {
		return fmt.Errorf("config set: %w", err)
	}

	defaultCache.invalidate(path)
	return nil
}

// encodeValueNode encodes a Go value into a yaml.Node, deriving the tag
// from the value's type.
//
// yaml.Node.Encode panics rather than returning an error for values yaml
// cannot marshal (channels, funcs, and anything containing them). A
// config setter must not take down its caller over a bad argument, so the
// panic is recovered and returned as an ordinary error.
func encodeValueNode(value any) (node *yaml.Node, err error) {
	defer func() {
		if r := recover(); r != nil {
			node = nil
			if e, ok := r.(error); ok {
				err = e
				return
			}
			err = fmt.Errorf("%v", r)
		}
	}()

	var n yaml.Node
	if err := n.Encode(value); err != nil {
		return nil, err
	}
	return &n, nil
}

// parseOrCreateDoc reads a YAML file as a yaml.Node document.
// If the file doesn't exist, returns a new empty document node.
func parseOrCreateDoc(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &yaml.Node{
				Kind: yaml.DocumentNode,
				Content: []*yaml.Node{
					{Kind: yaml.MappingNode},
				},
			}, nil
		}
		return nil, err
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return &yaml.Node{
			Kind: yaml.DocumentNode,
			Content: []*yaml.Node{
				{Kind: yaml.MappingNode},
			},
		}, nil
	}

	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(false)
	if err := dec.Decode(&doc); err != nil {
		if err == io.EOF {
			return &yaml.Node{
				Kind: yaml.DocumentNode,
				Content: []*yaml.Node{
					{Kind: yaml.MappingNode},
				},
			}, nil
		}
		return nil, err
	}
	return &doc, nil
}

// setNodeInMapping sets or updates a value node in a mapping node.
// If the key already exists, the existing value node is overwritten in
// place, carrying over any comments attached to it. Otherwise a new
// key-value pair is appended. The value node may be of any kind —
// scalar, sequence or mapping.
//
// The overwrite mutates the existing node rather than swapping the
// slice element for a new pointer, because yaml.v3 represents an alias
// as an AliasNode holding a pointer to the anchored node. Repointing
// the mapping entry leaves the anchored node reachable only from those
// alias pointers, so the encoder never walks it, never emits the `&a`
// definition, and still emits every `*a` reference — producing a file
// that fails to parse with "unknown anchor". Mutating in place keeps
// the anchored node in the tree, and Anchor is preserved explicitly so
// the definition survives the new value.
func setNodeInMapping(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			old := mapping.Content[i+1]
			if value.HeadComment == "" {
				value.HeadComment = old.HeadComment
			}
			if value.LineComment == "" {
				value.LineComment = old.LineComment
			}
			if value.FootComment == "" {
				value.FootComment = old.FootComment
			}
			anchor := old.Anchor
			*old = *value
			old.Anchor = anchor
			return
		}
	}
	mapping.Content = append(
		mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		value,
	)
}

// writeDoc marshals a yaml.Node document back to a file,
// preserving comments. Creates parent directories if missing.
func writeDoc(path string, doc *yaml.Node) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
