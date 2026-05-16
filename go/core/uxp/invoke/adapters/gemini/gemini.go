// Package gemini implements the InvocationAdapter for Gemini CLI.
//
// Help-text basis: gemini --version 0.40.1 (2026-05-09 capture at
// .tlc/tracks/uxp-agent-cli-facade/help/gemini.txt).
//
// Key shape:
//   - run: gemini -p "<prompt>"
//   - interactive: gemini [query]
//   - resume by id: gemini --resume <id>
//   - resume latest: gemini --resume latest
//   - no fork
package gemini

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

const Binary = "gemini"

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) CLI() uxp.CLIName { return uxp.CLIGemini }

func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("gemini: Mode %q is not valid", inv.Mode)
	}

	args := []string{}

	switch inv.Mode {
	case invoke.ModeInteractive:
		// no flag
	case invoke.ModeRun:
		// gemini takes the prompt via -p <text>; we'll add the value
		// after assembling the rest so file/image blocks can prepend.
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("gemini: ModeResume requires SessionID or Continue=true")
		}
		if inv.Continue {
			args = append(args, "--resume", "latest")
		} else {
			args = append(args, "--resume", inv.SessionID)
		}
	}

	if inv.Fork {
		return invoke.CommandSpec{}, ds,
			errors.New("gemini: Fork is unsupported (no native --fork-session)")
	}

	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	if inv.Agent != "" {
		// gemini has no --agent flag; refuse rather than silently drop.
		return invoke.CommandSpec{}, ds,
			errors.New("gemini: Agent is unsupported")
	}

	switch inv.Output {
	case invoke.OutputDefault, invoke.OutputText:
		// nothing
	case invoke.OutputJSON:
		args = append(args, "--output-format", "json")
	case invoke.OutputStreamJSON:
		args = append(args, "--output-format", "stream-json")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("gemini: Output %q is not valid", inv.Output)
	}

	switch inv.Approval {
	case invoke.ApprovalDefault, invoke.ApprovalAsk:
		// gemini default
	case invoke.ApprovalAutoEdit:
		args = append(args, "--approval-mode", "auto_edit")
	case invoke.ApprovalAutoAll:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Approval", string(inv.Approval),
				"--approval-mode yolo",
				[]string{"ApprovalAsk", "ApprovalAutoEdit"}))
			return invoke.CommandSpec{}, ds,
				errors.New("gemini: ApprovalAutoAll is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
		}
		args = append(args, "--approval-mode", "yolo")
	case invoke.ApprovalPlan:
		args = append(args, "--approval-mode", "plan")
	case invoke.ApprovalNever:
		// gemini has no never; closest is auto_edit (auto-accept edits,
		// still asks for shell). Refuse rather than shim ambiguously.
		return invoke.CommandSpec{}, ds,
			errors.New("gemini: ApprovalNever is unsupported (no equivalent to --permission-mode dontAsk)")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("gemini: Approval %q is not valid", inv.Approval)
	}

	switch inv.Sandbox {
	case invoke.SandboxDefault:
		// nothing
	case invoke.SandboxReadOnly:
		// shim via --approval-mode plan; if Approval=plan we already
		// added it.
		if !hasFlag(args, "--approval-mode") {
			args = append(args, "--approval-mode", "plan")
		}
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Sandbox",
			Message: "SandboxReadOnly shimmed via --approval-mode plan; not a true sandbox"})
	case invoke.SandboxWorkspaceWrite:
		args = append(args, "--sandbox")
		ds.Add(invoke.Diagnostic{Level: "info", Option: "Sandbox",
			Message: "SandboxWorkspaceWrite mapped to gemini's boolean --sandbox flag"})
	case invoke.SandboxDangerFullAccess:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Sandbox", string(inv.Sandbox),
				"--yolo",
				[]string{"SandboxWorkspaceWrite", "SandboxReadOnly"}))
			return invoke.CommandSpec{}, ds,
				errors.New("gemini: SandboxDangerFullAccess is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
		}
		args = append(args, "--yolo")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("gemini: Sandbox %q is not valid", inv.Sandbox)
	}

	// AddDirs: --include-directories is repeatable. Use one flag per
	// dir for clarity (alternative is a single comma-joined value).
	for _, d := range inv.AddDirs {
		args = append(args, "--include-directories", d)
	}

	// Files (S-1): reduce to parents and pass via --include-directories.
	if len(inv.Files) > 0 {
		parents := shim.ExpandToParentDirs(inv.Files)
		// Avoid double-listing dirs already in AddDirs.
		for _, p := range parents {
			if !slices.Contains(inv.AddDirs, p) {
				args = append(args, "--include-directories", p)
			}
		}
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Files",
			Message: fmt.Sprintf("Files reduced to %d parent dir(s) via --include-directories and listed in prompt block (S-1+S-3); gemini has no per-file flag",
				len(parents))})
	}

	if len(inv.Images) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Images",
			Message: fmt.Sprintf("%d image(s) listed in prompt block; gemini has no headless image flag (--prompt accepts text only)",
				len(inv.Images))})
	}

	args = appendConfigArgs(args, inv.Config, &ds)

	if inv.Debug {
		args = append(args, "-d")
	}
	if len(inv.ExtraArgs) > 0 {
		args = append(args, inv.ExtraArgs...)
	}

	prompt := composePrompt(inv)
	if inv.Mode == invoke.ModeRun {
		// -p takes the prompt as its value.
		args = append(args, "-p", prompt)
	} else if prompt != "" {
		// Interactive or resume: positional query.
		args = append(args, prompt)
	}

	return invoke.CommandSpec{
		Path: Binary,
		Args: args,
		Dir:  inv.CWD,
		Env:  inv.Env,
	}, ds, nil
}

func composePrompt(inv invoke.Invocation) string {
	var b strings.Builder
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
	const ns = "gemini."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "policy":
			for _, p := range shim.SplitConfigList(v) {
				args = append(args, "--policy", p)
			}
		case "admin_policy":
			for _, p := range shim.SplitConfigList(v) {
				args = append(args, "--admin-policy", p)
			}
		case "allowed_mcp_server_names":
			for _, n := range shim.SplitConfigList(v) {
				args = append(args, "--allowed-mcp-server-names", n)
			}
		case "extensions":
			for _, e := range shim.SplitConfigList(v) {
				args = append(args, "--extensions", e)
			}
		case "skip_trust":
			if configBoolStr(v) {
				args = append(args, "--skip-trust")
			}
		case "raw_output":
			if configBoolStr(v) {
				args = append(args, "--raw-output", "--accept-raw-output-risk")
			}
		case "screen_reader":
			if configBoolStr(v) {
				args = append(args, "--screen-reader")
			}
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown gemini Config key: %s", k)})
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

func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}
