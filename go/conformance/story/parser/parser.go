// Package parser reads a story YAML file off disk and decodes it
// into a *schema.Story using yaml.v3's KnownFields(true).
//
// KnownFields(true) is the structural enforcement of the closed-key
// set: any field not declared on schema.Story / schema.Step /
// schema.Reference fails the parse with a precise
// `field "X" not found in type schema.Story` error. This is the
// load-bearing leak-rule resistance — the four scenario shapes
// (scenario_id, assertions, cassette_must_*, judge) are simply not
// in the struct and therefore cannot survive a parse.
package parser

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/conformance/story/schema"
)

// ParsedStory carries both the typed struct and the raw root node so
// the validator can report file:line locations for findings. The
// raw node is decoded a second time (cheap) because yaml.v3 does not
// expose line numbers through the struct-decode path.
type ParsedStory struct {
	// Path is the file path the bytes were read from. "<bytes>" for
	// in-memory inputs (tests).
	Path string

	// Story is the typed, closed-key-validated struct.
	Story *schema.Story

	// Root is the raw yaml.Node tree rooted at the document mapping.
	// Validators consume Root to report file:line on findings.
	Root *yaml.Node
}

// ParseFile reads + parses a story YAML file. Returns a ParsedStory
// on success; on schema violation, returns an error whose message
// includes the file path and the offending field / line. A parse
// failure here is fatal — the validator's tier 1 layer expects a
// fully decoded struct.
func ParseFile(path string) (*ParsedStory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read story %s: %w", path, err)
	}
	return ParseBytes(data, path)
}

// ParseBytes parses an in-memory story YAML blob. label is the
// human-readable source identifier used in error messages (file
// path, "<bytes>", etc.).
func ParseBytes(data []byte, label string) (*ParsedStory, error) {
	if label == "" {
		label = "<bytes>"
	}

	// Decode into the typed struct with KnownFields(true). This is
	// the structural leak-rule resistance: any top-level / step /
	// reference key not declared on the Story struct fails the
	// parse here. Re-applied at every Decode call site in this
	// package — do not relax.
	var story schema.Story
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&story); err != nil {
		return nil, fmt.Errorf("parse story %s: %w", label, err)
	}

	// Decode the same bytes a second time into a yaml.Node tree so
	// the validator can report file:line. yaml.v3's struct-decode
	// path drops node positions; a second pass is the cheapest way
	// to recover them. We do NOT apply KnownFields to the node
	// decode (the typed decode above already enforced that).
	var rootDoc yaml.Node
	if err := yaml.Unmarshal(data, &rootDoc); err != nil {
		// If KnownFields-strict typed decode succeeded but a plain
		// node decode fails, treat as IO-style malformed input.
		return nil, fmt.Errorf("re-parse story %s for node positions: %w", label, err)
	}

	return &ParsedStory{
		Path:  label,
		Story: &story,
		Root:  &rootDoc,
	}, nil
}
