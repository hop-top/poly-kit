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
	setScalarInMapping(parent, leafKey, value)

	if err := writeDoc(path, doc); err != nil {
		return fmt.Errorf("config set: %w", err)
	}

	defaultCache.invalidate(path)
	return nil
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

// setScalarInMapping sets or updates a scalar value in a mapping node.
// If the key already exists, updates the value. Otherwise appends a
// new key-value pair.
func setScalarInMapping(mapping *yaml.Node, key, value string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Tag = "!!str"
			mapping.Content[i+1].Kind = yaml.ScalarNode
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value, Tag: "!!str"},
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
