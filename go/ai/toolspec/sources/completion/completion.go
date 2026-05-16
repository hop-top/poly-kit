// Package completion extracts tool metadata from shell completion scripts.
package completion

import (
	"regexp"
	"strings"

	"hop.top/kit/go/ai/toolspec"
)

// zshCmdRe matches 'name:description' or "name:description" entries
// typically found inside zsh completion arrays.
var zshCmdRe = regexp.MustCompile(`['"]([a-zA-Z][\w-]*):([^'"]+)['"]`)

// zshLongFlagRe matches '--flag[Description]' patterns.
var zshLongFlagRe = regexp.MustCompile(`'--([a-zA-Z][\w-]*)\[([^\]]+)\]'`)

// zshShortLongRe matches '(-s --long)'{-s,--long}'[Description]' patterns.
var zshShortLongRe = regexp.MustCompile(
	`'\(-(\w)\s+--([a-zA-Z][\w-]*)\)'\{-\w,--[a-zA-Z][\w-]*\}'\[([^\]]+)\]'`,
)

// ParseZshCompletion parses a zsh completion script and returns a ToolSpec
// populated with extracted commands and flags.
func ParseZshCompletion(name, script string) *toolspec.ToolSpec {
	ts := &toolspec.ToolSpec{Name: name}

	// Extract commands from 'cmd:desc' patterns.
	for _, m := range zshCmdRe.FindAllStringSubmatch(script, -1) {
		ts.Commands = append(ts.Commands, toolspec.Command{
			Name: m[1],
		})
	}

	// Extract flags — try short+long first, then long-only.
	seen := map[string]bool{}

	for _, m := range zshShortLongRe.FindAllStringSubmatch(script, -1) {
		long := m[2]
		if seen[long] {
			continue
		}
		seen[long] = true
		ts.Flags = append(ts.Flags, toolspec.Flag{
			Name:        "--" + long,
			Short:       "-" + m[1],
			Description: m[3],
		})
	}

	for _, m := range zshLongFlagRe.FindAllStringSubmatch(script, -1) {
		long := m[1]
		if seen[long] {
			continue
		}
		seen[long] = true
		ts.Flags = append(ts.Flags, toolspec.Flag{
			Name:        "--" + long,
			Description: m[2],
		})
	}

	return ts
}

// bashOptsRe matches opts="cmd1 cmd2 ..." or similar variable assignments.
var bashOptsRe = regexp.MustCompile(`(?:opts|commands|cmds)="([^"]+)"`)

// bashCaseRe matches individual case-statement arms like: cmd) ... ;;
var bashCaseRe = regexp.MustCompile(`(?m)^\s+(\w[\w-]*)\)`)

// ParseBashCompletion parses a bash completion script and returns a ToolSpec
// populated with extracted command names.
func ParseBashCompletion(name, script string) *toolspec.ToolSpec {
	ts := &toolspec.ToolSpec{Name: name}
	seen := map[string]bool{}

	// Try opts/commands variable first.
	for _, m := range bashOptsRe.FindAllStringSubmatch(script, -1) {
		for _, word := range strings.Fields(m[1]) {
			w := strings.TrimSpace(word)
			if w == "" || seen[w] {
				continue
			}
			seen[w] = true
			ts.Commands = append(ts.Commands, toolspec.Command{Name: w})
		}
	}

	// Fallback: case statement arms.
	if len(ts.Commands) == 0 {
		for _, m := range bashCaseRe.FindAllStringSubmatch(script, -1) {
			cmd := m[1]
			if seen[cmd] {
				continue
			}
			seen[cmd] = true
			ts.Commands = append(ts.Commands, toolspec.Command{Name: cmd})
		}
	}

	return ts
}
