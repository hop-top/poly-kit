package help

import (
	"regexp"
	"strings"

	"hop.top/kit/go/ai/toolspec"
)

var (
	deprecatedRe = regexp.MustCompile(`(?i)\bdeprecated\b`)
	replacedByRe = regexp.MustCompile(`(?i)(?:use\s+(\S+)\s+instead|replaced\s+by\s+(\S+))`)
	envVarRe     = regexp.MustCompile(`\b([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)\b`)
	configCmdRe  = regexp.MustCompile(`(?i)^(config|settings|configure|setup)$`)
	authCmdRe    = regexp.MustCompile(`(?i)^(auth|login|logout|whoami)$`)
)

// envVarExclusions filters false-positive env var matches (section headers).
var envVarExclusions = map[string]bool{
	"FLAGS": true, "OPTIONS": true, "USAGE": true, "COMMANDS": true,
	"GLOBAL_FLAGS": true, "GLOBAL_OPTIONS": true, "EXAMPLES": true,
	"DESCRIPTION": true, "SYNOPSIS": true, "ENVIRONMENT": true,
	"SEE_ALSO": true, "HELP_TOPICS": true, "LEARN_MORE": true,
	"AVAILABLE_COMMANDS": true, "CORE_COMMANDS": true,
	"ADDITIONAL_COMMANDS": true, "MANAGEMENT_COMMANDS": true,
	"RESOURCES": true, "DIAGNOSTICS": true,
}

// EnrichFromHelp applies post-parse heuristics to fill in deprecation,
// output schema, and state introspection fields.
func EnrichFromHelp(spec *toolspec.ToolSpec, helpText string) {
	enrichDeprecation(spec, helpText)
	enrichOutputSchema(spec)
	enrichStateIntrospection(spec, helpText)
}

func enrichDeprecation(spec *toolspec.ToolSpec, helpText string) {
	for i := range spec.Flags {
		markFlagDeprecation(&spec.Flags[i])
	}
	lines := strings.Split(helpText, "\n")
	for i := range spec.Commands {
		enrichCommandDeprecation(&spec.Commands[i], lines)
	}
}

func markFlagDeprecation(f *toolspec.Flag) {
	if !deprecatedRe.MatchString(f.Description) {
		return
	}
	f.Deprecated = true
	if m := replacedByRe.FindStringSubmatch(f.Description); m != nil {
		f.ReplacedBy = coalesce(m[1], m[2])
	}
}

func enrichCommandDeprecation(cmd *toolspec.Command, lines []string) {
	for i := range cmd.Flags {
		markFlagDeprecation(&cmd.Flags[i])
	}
	for _, line := range lines {
		if strings.Contains(line, cmd.Name) && deprecatedRe.MatchString(line) {
			cmd.Deprecated = true
			if m := replacedByRe.FindStringSubmatch(line); m != nil {
				cmd.ReplacedBy = coalesce(m[1], m[2])
			}
			break
		}
	}
	for i := range cmd.Children {
		enrichCommandDeprecation(&cmd.Children[i], lines)
	}
}

func enrichOutputSchema(spec *toolspec.ToolSpec) {
	hasJSON, hasFormat := scanFlags(spec.Flags)
	for i := range spec.Commands {
		enrichCommandOutput(&spec.Commands[i], hasJSON, hasFormat)
	}
}

func enrichCommandOutput(cmd *toolspec.Command, parentJSON, parentFmt bool) {
	if cmd.OutputSchema != nil {
		return
	}
	localJSON, localFmt := scanFlags(cmd.Flags)
	if localJSON || parentJSON {
		cmd.OutputSchema = &toolspec.OutputSchema{Format: "json"}
	} else if localFmt || parentFmt {
		cmd.OutputSchema = &toolspec.OutputSchema{Format: "custom"}
	}
	for i := range cmd.Children {
		enrichCommandOutput(&cmd.Children[i], parentJSON || localJSON, parentFmt || localFmt)
	}
}

func scanFlags(flags []toolspec.Flag) (json, format bool) {
	for _, f := range flags {
		switch f.Name {
		case "--json":
			json = true
		case "--format":
			format = true
		}
	}
	return
}

func enrichStateIntrospection(spec *toolspec.ToolSpec, helpText string) {
	var si toolspec.StateIntrospection
	for _, cmd := range spec.Commands {
		collectIntrospectionCmds(&cmd, &si)
	}
	seen := make(map[string]bool)
	for _, m := range envVarRe.FindAllString(helpText, -1) {
		if !envVarExclusions[m] && !seen[m] {
			seen[m] = true
			si.EnvVars = append(si.EnvVars, m)
		}
	}
	if len(si.ConfigCommands)+len(si.EnvVars)+len(si.AuthCommands) > 0 {
		spec.StateIntrospection = &si
	}
}

func collectIntrospectionCmds(cmd *toolspec.Command, si *toolspec.StateIntrospection) {
	if configCmdRe.MatchString(cmd.Name) {
		si.ConfigCommands = append(si.ConfigCommands, cmd.Name)
	}
	if authCmdRe.MatchString(cmd.Name) {
		si.AuthCommands = append(si.AuthCommands, cmd.Name)
	}
	for i := range cmd.Children {
		collectIntrospectionCmds(&cmd.Children[i], si)
	}
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
