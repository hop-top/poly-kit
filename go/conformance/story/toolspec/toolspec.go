// Package toolspec is a minimal projection of the kit toolspec shape
// — just enough to walk command trees + resolve flags for tier 3 of
// the story validator. We deliberately load a subset, not the full
// surface: the story validator only needs commands[] (for command
// resolution) and flags[] (for flag resolution).
//
// Other toolspec fields (intent, contract, safety, output_schema) are
// ignored here. If kit later ships a typed toolspec parser as a
// public API, this package migrates to consume it without schema
// change at the story side.
package toolspec

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Toolspec is the minimal projection of a tool's toolspec YAML.
type Toolspec struct {
	Name          string    `yaml:"name"`
	SchemaVersion string    `yaml:"schema_version"`
	Commands      []Command `yaml:"commands"`
}

// Command is a tree node. Children is the recursive case; Flags is
// the per-command flag list the story validator resolves against.
type Command struct {
	Name     string    `yaml:"name"`
	Children []Command `yaml:"children,omitempty"`
	Flags    []Flag    `yaml:"flags,omitempty"`
}

// Flag is the minimal projection of a flag declaration.
type Flag struct {
	Name  string `yaml:"name"`
	Short string `yaml:"short,omitempty"`
	Type  string `yaml:"type,omitempty"`
}

// LoadFromPath reads + parses a toolspec YAML file. Unknown top-level
// keys (intent, contract, safety, etc.) are tolerated — this is a
// projection, not a full parser. Only the closed subset we care
// about is decoded.
func LoadFromPath(path string) (*Toolspec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read toolspec %s: %w", path, err)
	}
	var t Toolspec
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse toolspec %s: %w", path, err)
	}
	return &t, nil
}

// ResolveCommand walks tokens against the toolspec command tree and
// returns the matched leaf (or the deepest matched node) plus a
// boolean signal. Tokens are matched left-to-right; flag tokens
// (anything starting with "-") are skipped — caller resolves flags
// separately via ResolveFlag.
//
// A non-flag token that does not match any child of the current node
// is treated as a flag value or positional argument (which we can't
// distinguish without flag-type info), and the walk stops at the
// current matched command. The boolean is true whenever at least one
// command was matched.
//
// The boolean is false ONLY when the very first non-flag token does
// not resolve to a top-level command, which is the case the story
// validator wants to surface as "unknown-command".
func (t *Toolspec) ResolveCommand(tokens []string) (*Command, bool) {
	if t == nil {
		return nil, false
	}
	cur := t.Commands
	var matched *Command
	for _, tok := range tokens {
		if len(tok) > 0 && tok[0] == '-' {
			continue
		}
		var hit *Command
		for i := range cur {
			if cur[i].Name == tok {
				hit = &cur[i]
				break
			}
		}
		if hit == nil {
			// No further descent; remaining tokens are flag values
			// or positional arguments. ok is true only if we
			// matched at least one command (otherwise the first
			// non-flag token was unknown and we have nothing).
			return matched, matched != nil
		}
		matched = hit
		cur = hit.Children
	}
	return matched, matched != nil
}

// GlobalFlags lists the kit-wide flags that every command implicitly
// accepts. These are hardcoded in v1; a future v1.x can load them
// from a `globals:` section of the toolspec.
var GlobalFlags = map[string]struct{}{
	"--format":   {},
	"--output":   {},
	"--profile":  {},
	"--verbose":  {},
	"--quiet":    {},
	"--no-color": {},
	"--help":     {},
	"-h":         {},
	"-V":         {},
	"-v":         {},
	"--version":  {},
}

// ResolveFlag reports whether flag is declared on cmd or in the
// global allowlist. flag is the raw token from invoke[] (e.g.
// "--payload" or "-o"); values are not consulted.
//
// stripVal trims "=value" suffixes ("--payload=alpha" → "--payload")
// so flag-with-value tokens resolve cleanly.
func ResolveFlag(cmd *Command, flag string) bool {
	bare := stripVal(flag)
	if _, ok := GlobalFlags[bare]; ok {
		return true
	}
	if cmd == nil {
		return false
	}
	for _, f := range cmd.Flags {
		if f.Name == bare || (f.Short != "" && f.Short == bare) {
			return true
		}
	}
	return false
}

// stripVal removes the "=value" suffix from a flag token. Returns
// the unmodified input when no "=" is present.
func stripVal(flag string) string {
	for i := 0; i < len(flag); i++ {
		if flag[i] == '=' {
			return flag[:i]
		}
	}
	return flag
}
