// Package cursoragent implements the InvocationAdapter for Cursor
// Agent CLI.
//
// Help-text basis: cursor-agent 2025.10.01-f425367 (2026-05-09 capture
// at .tlc/tracks/uxp-agent-cli-facade/help/cursor-agent.txt).
//
// Distinctive shape:
//   - run: cursor-agent -p "<prompt>"
//   - interactive: cursor-agent [prompt...]
//   - resume by id: cursor-agent --resume <id>
//   - resume latest: cursor-agent resume (subcommand)
//   - no fork
//   - --output-format text|json|stream-json works ONLY with --print
//   - sandbox is a config subcommand, not per-invocation; Sandbox*
//     options are refused
//   - -f/--force is auto-all only (refuses ApprovalAutoEdit)
package cursoragent

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

const Binary = "cursor-agent"

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) CLI() uxp.CLIName { return uxp.CLICursorAgent }

func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("cursor-agent: Mode %q is not valid", inv.Mode)
	}
	if inv.Fork {
		return invoke.CommandSpec{}, ds, errors.New("cursor-agent: Fork is unsupported")
	}
	if inv.Agent != "" {
		return invoke.CommandSpec{}, ds, errors.New("cursor-agent: Agent is unsupported")
	}

	args := []string{}

	switch inv.Mode {
	case invoke.ModeInteractive:
		// no flag
	case invoke.ModeRun:
		args = append(args, "-p")
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("cursor-agent: ModeResume requires SessionID or Continue=true")
		}
		if inv.Continue {
			// Resume latest is a subcommand: cursor-agent resume.
			args = append(args, "resume")
		} else {
			args = append(args, "--resume", inv.SessionID, "-p")
		}
	}

	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}

	switch inv.Output {
	case invoke.OutputDefault, invoke.OutputText:
		// nothing
	case invoke.OutputJSON, invoke.OutputStreamJSON:
		if inv.Mode == invoke.ModeInteractive {
			return invoke.CommandSpec{}, ds, fmt.Errorf(
				"cursor-agent: Output=%q requires --print (ModeRun or ModeResume)", inv.Output)
		}
		switch inv.Output {
		case invoke.OutputJSON:
			args = append(args, "--output-format", "json")
		case invoke.OutputStreamJSON:
			args = append(args, "--output-format", "stream-json")
		}
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("cursor-agent: Output %q is not valid", inv.Output)
	}

	switch inv.Sandbox {
	case invoke.SandboxDefault:
		// nothing
	case invoke.SandboxReadOnly, invoke.SandboxWorkspaceWrite:
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"cursor-agent: Sandbox=%q is unsupported (sandbox is configured via the cursor-agent sandbox subcommand, not per-invocation)",
			inv.Sandbox)
	case invoke.SandboxDangerFullAccess:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Sandbox", string(inv.Sandbox),
				"-f/--force",
				[]string{"SandboxDefault"}))
			return invoke.CommandSpec{}, ds, errors.New(
				"cursor-agent: SandboxDangerFullAccess is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
		}
		args = append(args, "-f")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("cursor-agent: Sandbox %q is not valid", inv.Sandbox)
	}

	switch inv.Approval {
	case invoke.ApprovalDefault, invoke.ApprovalAsk:
		// nothing
	case invoke.ApprovalAutoEdit:
		ds.Add(shim.RefuseDangerousDegradation(
			"Approval", string(inv.Approval),
			"-f/--force",
			[]string{"ApprovalAsk"}))
		return invoke.CommandSpec{}, ds, errors.New(
			"cursor-agent: ApprovalAutoEdit is unsupported (-f/--force is auto-all only)")
	case invoke.ApprovalAutoAll:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Approval", string(inv.Approval),
				"-f/--force",
				[]string{"ApprovalAsk"}))
			return invoke.CommandSpec{}, ds, errors.New(
				"cursor-agent: ApprovalAutoAll requires Config[\"uxp.allow_dangerous\"]=\"true\" (maps to -f/--force)")
		}
		if !slices.Contains(args, "-f") {
			args = append(args, "-f")
		}
	case invoke.ApprovalPlan, invoke.ApprovalNever:
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"cursor-agent: Approval=%q is unsupported", inv.Approval)
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("cursor-agent: Approval %q is not valid", inv.Approval)
	}

	// AddDirs / Files / Images: cursor-agent has no native flag for
	// any of these. All go through the prompt-block (S-3).
	addDirsAndFilesShim(&ds, &inv)
	if len(inv.Images) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Images",
			Message: fmt.Sprintf("%d image(s) listed in prompt block; cursor-agent has no headless image flag", len(inv.Images))})
	}

	args = appendConfigArgs(args, inv.Config, &ds)

	if len(inv.ExtraArgs) > 0 {
		args = append(args, inv.ExtraArgs...)
	}

	prompt := composePrompt(inv)
	if prompt != "" && inv.Mode != invoke.ModeResume {
		args = append(args, prompt)
	} else if prompt != "" && inv.Mode == invoke.ModeResume {
		// resume subcommand or --resume <id>: positional follow-up.
		args = append(args, prompt)
	}

	return invoke.CommandSpec{
		Path: Binary,
		Args: args,
		Dir:  inv.CWD,
		Env:  inv.Env,
	}, ds, nil
}

func addDirsAndFilesShim(ds *invoke.Diagnostics, inv *invoke.Invocation) {
	if len(inv.AddDirs) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "AddDirs",
			Message: fmt.Sprintf("%d AddDirs listed in prompt block; cursor-agent has no --add-dir flag", len(inv.AddDirs))})
	}
	if len(inv.Files) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Files",
			Message: fmt.Sprintf("%d Files listed in prompt block; cursor-agent has no per-file flag", len(inv.Files))})
	}
}

func composePrompt(inv invoke.Invocation) string {
	var b strings.Builder
	if len(inv.AddDirs) > 0 {
		fmt.Fprintf(&b, "Workspace directories:\n")
		for _, d := range inv.AddDirs {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteString("\n")
	}
	if len(inv.Files) > 0 {
		b.WriteString(shim.FormatFileBlock(inv.Files, inv.CWD))
		b.WriteString("\n")
	}
	if len(inv.Images) > 0 {
		fmt.Fprintf(&b, "Images relevant to this task:\n")
		for _, img := range inv.Images {
			fmt.Fprintf(&b, "- %s\n", img)
		}
		b.WriteString("\n")
	}
	b.WriteString(inv.Prompt)
	return b.String()
}

func appendConfigArgs(args []string, cfg map[string]string, ds *invoke.Diagnostics) []string {
	const ns = "cursor."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "api_key":
			args = append(args, "--api-key", v)
		case "background":
			if configBoolStr(v) {
				args = append(args, "-b")
			}
		case "stream_partial_output":
			if configBoolStr(v) {
				args = append(args, "--stream-partial-output")
			}
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown cursor Config key: %s", k)})
		}
	}
	return args
}

func configBool(cfg map[string]string, key string) bool {
	if cfg == nil {
		return false
	}
	v, ok := cfg[key]
	if !ok {
		return false
	}
	return configBoolStr(v)
}

func configBoolStr(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
