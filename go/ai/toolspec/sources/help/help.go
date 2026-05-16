// Package help extracts ToolSpec data from CLI --help output.
package help

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"hop.top/kit/go/ai/toolspec"
)

// DefaultTimeout is the max duration for a --help invocation.
const DefaultTimeout = 5 * time.Second

// HelpSource resolves a ToolSpec by running <tool> --help.
type HelpSource struct {
	// Timeout overrides the default 5s deadline. Zero uses DefaultTimeout.
	Timeout time.Duration
}

// Resolve runs the tool's --help and parses the output.
func (s *HelpSource) Resolve(tool string) (*toolspec.ToolSpec, error) {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, tool, "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Many tools exit non-zero for --help; use output if present.
		if len(out) == 0 {
			return nil, err
		}
	}
	return ParseHelpOutput(tool, string(out)), nil
}

// Section headers that introduce commands.
var commandHeaders = regexp.MustCompile(
	`(?i)^(available commands|core commands|additional commands|management commands|commands):?\s*$`,
)

// Section headers that introduce flags/options.
var flagHeaders = regexp.MustCompile(
	`(?i)^(flags|global flags|options|global options):?\s*$`,
)

// allCapsHeader matches ALL-CAPS section headers (wrangler, gh style).
var allCapsHeader = regexp.MustCompile(`^[A-Z][A-Z &]+$`)

// nonCommandSections are ALL-CAPS headers that list flags/options, not commands.
var nonCommandSections = map[string]bool{
	"FLAGS": true, "GLOBAL FLAGS": true, "GLOBAL OPTIONS": true,
	"OPTIONS": true, "USAGE": true, "EXAMPLES": true,
	"HELP TOPICS": true, "LEARN MORE": true, "RESOURCES": true,
	"DESCRIPTION": true, "SYNOPSIS": true, "ENVIRONMENT": true,
	"SEE ALSO": true, "DIAGNOSTICS": true,
}

// anyHeader matches lines that look like section headers:
// uppercase-start word(s) optionally followed by colon.
// Case-sensitive: lowercase narrative lines (e.g. "start a working area")
// must not be treated as headers.
var anyHeader = regexp.MustCompile(`^[A-Z][A-Za-z /]+:?\s*$`)

// Matches a command line: leading whitespace, name, then spaces + description.
//
//	"  clone  Clone a repository"
//	"  browse:     Open the repository in the browser"
var commandLine = regexp.MustCompile(`^\s{2,}(\S+):?\s{2,}(.+)$`)

// Matches a flag line like "  -f, --flag  desc" with optional value placeholder.
var flagLine = regexp.MustCompile(
	`^\s{2,}(-\w),?\s+(--[\w][\w-]*)(?:\s+<\w+>)?\s{2,}(.+)$`,
)

// Matches a long-only flag: "  --flag  desc" (no short).
var flagLongOnly = regexp.MustCompile(
	`^\s{2,}(--[\w][\w-]*)(?:\s+<\w+>)?\s{2,}(.+)$`,
)

// gitNarrative detects git-style preamble that precedes indented commands.
var gitNarrative = regexp.MustCompile(
	`(?i)^these are common .+ commands`,
)

// ParseHelpOutput parses common --help formats and returns a ToolSpec.
func ParseHelpOutput(name, output string) *toolspec.ToolSpec {
	spec := &toolspec.ToolSpec{Name: name}

	lines := strings.Split(output, "\n")
	var mode string // "commands" | "flags" | ""

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")

		// Detect section transitions.
		if commandHeaders.MatchString(trimmed) {
			mode = "commands"
			continue
		}
		if flagHeaders.MatchString(trimmed) {
			mode = "flags"
			continue
		}

		// git-style: "These are common Git commands..." starts commands.
		if gitNarrative.MatchString(trimmed) {
			mode = "commands"
			continue
		}

		// ALL-CAPS headers (wrangler/gh style): treat as command section
		// unless it's a known non-command section.
		if allCapsHeader.MatchString(trimmed) &&
			!commandHeaders.MatchString(trimmed) &&
			!flagHeaders.MatchString(trimmed) {
			if nonCommandSections[trimmed] {
				mode = "flags"
			} else {
				mode = "commands"
			}
			continue
		}

		// A new unrecognized header ends the current section.
		if anyHeader.MatchString(trimmed) &&
			!commandHeaders.MatchString(trimmed) &&
			!flagHeaders.MatchString(trimmed) &&
			!gitNarrative.MatchString(trimmed) {
			mode = ""
			continue
		}

		// Skip non-indented, non-blank lines inside commands mode
		// (git-style narrative sub-headers like "start a working area").
		if mode == "commands" && trimmed != "" &&
			!strings.HasPrefix(trimmed, " ") &&
			!strings.HasPrefix(trimmed, "\t") {
			continue
		}

		switch mode {
		case "commands":
			if m := commandLine.FindStringSubmatch(trimmed); m != nil {
				cmdName := strings.TrimSuffix(m[1], ":")
				// Handle tool-prefixed lines: "wrangler docs [args]  Desc"
				// If first word matches tool name, use second word.
				if cmdName == name {
					parts := strings.Fields(strings.TrimSpace(trimmed))
					if len(parts) >= 2 {
						cmdName = strings.TrimSuffix(parts[1], ":")
					}
				}
				// Skip bracket/angle-bracket args like "[search..]"
				if strings.HasPrefix(cmdName, "[") || strings.HasPrefix(cmdName, "<") {
					continue
				}
				spec.Commands = append(spec.Commands, toolspec.Command{
					Name: cmdName,
				})
			}
		case "flags":
			if m := flagLine.FindStringSubmatch(trimmed); m != nil {
				f := toolspec.Flag{
					Name:        m[2],
					Short:       m[1],
					Description: strings.TrimSpace(m[3]),
				}
				spec.Flags = append(spec.Flags, f)
			} else if m := flagLongOnly.FindStringSubmatch(trimmed); m != nil {
				f := toolspec.Flag{
					Name:        m[1],
					Description: strings.TrimSpace(m[2]),
				}
				spec.Flags = append(spec.Flags, f)
			}
		}
	}

	inferContracts(spec)
	inferSafety(spec)
	inferPreviewModes(spec)
	EnrichFromHelp(spec, output)

	return spec
}

// destructiveNames are command names that imply destructive side effects.
var destructiveNames = map[string]bool{
	"delete": true, "remove": true, "rm": true, "destroy": true,
	"purge": true, "drop": true, "kill": true, "erase": true,
	"wipe": true, "clean": true,
}

// inferContracts sets Contract on commands based on name/flag heuristics.
func inferContracts(spec *toolspec.ToolSpec) {
	for i := range spec.Commands {
		c := &spec.Commands[i]
		if destructiveNames[c.Name] && c.Contract == nil {
			c.Contract = &toolspec.Contract{
				SideEffects: []string{"destructive"},
			}
		}
		for _, f := range c.Flags {
			if f.Name == "--force" || f.Name == "--dry-run" {
				if c.Contract == nil {
					c.Contract = &toolspec.Contract{}
				}
				c.Contract.Retryable = true
				break
			}
		}
	}
	// Also check top-level flags.
	for i := range spec.Commands {
		c := &spec.Commands[i]
		for _, f := range spec.Flags {
			if f.Name == "--force" || f.Name == "--dry-run" {
				if c.Contract == nil {
					c.Contract = &toolspec.Contract{}
				}
				c.Contract.Retryable = true
				break
			}
		}
	}
}

// inferSafety sets Safety on commands based on name/flag heuristics.
func inferSafety(spec *toolspec.ToolSpec) {
	for i := range spec.Commands {
		c := &spec.Commands[i]
		if c.Safety != nil {
			continue
		}
		if destructiveNames[c.Name] {
			c.Safety = &toolspec.Safety{Level: toolspec.SafetyLevelDangerous}
		} else {
			c.Safety = &toolspec.Safety{Level: toolspec.SafetyLevelSafe}
		}
		// Check command-level and top-level flags for confirmation hints.
		// Build a combined slice to avoid mutating c.Flags' backing array.
		allFlags := make([]toolspec.Flag, 0, len(c.Flags)+len(spec.Flags))
		allFlags = append(allFlags, c.Flags...)
		allFlags = append(allFlags, spec.Flags...)
		for _, f := range allFlags {
			if f.Name == "--yes" || f.Name == "--force" || f.Short == "-y" {
				c.Safety.RequiresConfirmation = true
				break
			}
		}
	}
}

// previewFlagMap maps flag names to preview mode strings.
var previewFlagMap = map[string]string{
	"--dry-run": "dryrun",
	"-n":        "dryrun",
	"--no-op":   "dryrun",
	"--plan":    "plan",
	"--diff":    "diff",
	"--explain": "explain",
}

// inferPreviewModes populates PreviewModes from flag heuristics.
func inferPreviewModes(spec *toolspec.ToolSpec) {
	allFlags := make([]toolspec.Flag, 0, len(spec.Flags))
	allFlags = append(allFlags, spec.Flags...)

	for i := range spec.Commands {
		c := &spec.Commands[i]
		combined := append(allFlags, c.Flags...)
		seen := map[string]bool{}
		for _, f := range combined {
			if mode, ok := previewFlagMap[f.Name]; ok && !seen[mode] {
				seen[mode] = true
				c.PreviewModes = append(c.PreviewModes, mode)
			}
			if mode, ok := previewFlagMap[f.Short]; ok && !seen[mode] {
				seen[mode] = true
				c.PreviewModes = append(c.PreviewModes, mode)
			}
		}
	}
}
