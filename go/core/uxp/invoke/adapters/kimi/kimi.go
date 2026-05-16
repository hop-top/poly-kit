// Package kimi implements the InvocationAdapter for Kimi Code CLI.
//
// Help-text basis: kimi (Moonshot AI) — see
// .tlc/tracks/uxp-agent-cli-facade/help/kimi.txt (2026-05-09 capture).
//
// Distinctive shape:
//   - Native --plan flag for ApprovalPlan (no shim needed).
//   - Native --agent default|okabe + --agent-file FILE.
//   - --yolo/--afk for ApprovalAutoAll (both auto-all variants).
//   - --output-format text|stream-json (no json choice); OutputJSON
//     shimmed via --print --output-format text --final-message-only
//     (alias --quiet).
//   - --add-dir + --skills-dir for AddDirs.
//   - --work-dir/-w for CWD (native).
//   - No native sandbox flag.
package kimi

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

const Binary = "kimi"

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) CLI() uxp.CLIName { return uxp.CLIKimi }

func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("kimi: Mode %q is not valid", inv.Mode)
	}
	if inv.Fork {
		return invoke.CommandSpec{}, ds, errors.New("kimi: Fork is unsupported")
	}

	args := []string{}

	switch inv.Mode {
	case invoke.ModeInteractive:
		// no flag
	case invoke.ModeRun:
		args = append(args, "--print")
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("kimi: ModeResume requires SessionID or Continue=true")
		}
		args = append(args, "--print")
		if inv.Continue {
			args = append(args, "-C")
		} else {
			args = append(args, "-S", inv.SessionID)
		}
	}

	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	if inv.Agent != "" {
		args = append(args, "--agent", inv.Agent)
	}

	if inv.CWD != "" {
		args = append(args, "--work-dir", inv.CWD)
	}

	switch inv.Output {
	case invoke.OutputDefault, invoke.OutputText:
		// nothing
	case invoke.OutputJSON:
		// kimi has no json choice; --quiet alias = --print
		// --output-format text --final-message-only.
		if !slices.Contains(args, "--print") {
			args = append(args, "--print")
		}
		args = append(args, "--output-format", "text", "--final-message-only")
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Output",
			Message: "kimi has no json output-format; OutputJSON shimmed via --print --output-format text --final-message-only (alias --quiet) — final assistant message as plain text"})
	case invoke.OutputStreamJSON:
		args = append(args, "--output-format", "stream-json")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("kimi: Output %q is not valid", inv.Output)
	}

	switch inv.Approval {
	case invoke.ApprovalDefault, invoke.ApprovalAsk:
		// nothing
	case invoke.ApprovalAutoEdit:
		ds.Add(shim.RefuseDangerousDegradation(
			"Approval", string(inv.Approval),
			"--yolo (or --afk)",
			[]string{"ApprovalAsk", "ApprovalPlan"}))
		return invoke.CommandSpec{}, ds, errors.New(
			"kimi: ApprovalAutoEdit is unsupported (--yolo and --afk are auto-all only)")
	case invoke.ApprovalAutoAll:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Approval", string(inv.Approval),
				"--yolo",
				[]string{"ApprovalAsk", "ApprovalPlan"}))
			return invoke.CommandSpec{}, ds, errors.New(
				"kimi: ApprovalAutoAll requires Config[\"uxp.allow_dangerous\"]=\"true\" (maps to --yolo)")
		}
		args = append(args, "--yolo")
	case invoke.ApprovalPlan:
		args = append(args, "--plan")
	case invoke.ApprovalNever:
		return invoke.CommandSpec{}, ds, errors.New(
			"kimi: ApprovalNever is unsupported")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("kimi: Approval %q is not valid", inv.Approval)
	}

	switch inv.Sandbox {
	case invoke.SandboxDefault:
		// nothing
	case invoke.SandboxReadOnly, invoke.SandboxWorkspaceWrite, invoke.SandboxDangerFullAccess:
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"kimi: Sandbox=%q is unsupported (no per-invocation sandbox flag)", inv.Sandbox)
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("kimi: Sandbox %q is not valid", inv.Sandbox)
	}

	for _, d := range inv.AddDirs {
		args = append(args, "--add-dir", d)
	}

	if len(inv.Files) > 0 {
		parents := shim.ExpandToParentDirs(inv.Files)
		for _, p := range parents {
			if !slices.Contains(inv.AddDirs, p) {
				args = append(args, "--add-dir", p)
			}
		}
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Files",
			Message: fmt.Sprintf("Files reduced to %d parent dir(s) via --add-dir and listed in prompt block (S-1+S-3)", len(parents))})
	}

	if len(inv.Images) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Images",
			Message: fmt.Sprintf("%d image(s) listed in prompt block; kimi has no headless image flag", len(inv.Images))})
	}

	args = appendConfigArgs(args, inv.Config, &ds)

	if len(inv.ExtraArgs) > 0 {
		args = append(args, inv.ExtraArgs...)
	}

	prompt := composePrompt(inv)
	if prompt != "" {
		args = append(args, "--prompt", prompt)
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
	const ns = "kimi."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "thinking":
			if configBoolStr(v) {
				args = append(args, "--thinking")
			} else {
				args = append(args, "--no-thinking")
			}
		case "afk":
			if configBoolStr(v) {
				args = append(args, "--afk")
			}
		case "agent_file":
			args = append(args, "--agent-file", v)
		case "skills_dir":
			for _, d := range shim.SplitConfigList(v) {
				args = append(args, "--skills-dir", d)
			}
		case "max_steps_per_turn":
			args = append(args, "--max-steps-per-turn", v)
		case "max_retries_per_step":
			args = append(args, "--max-retries-per-step", v)
		case "max_ralph_iterations":
			args = append(args, "--max-ralph-iterations", v)
		case "mcp_config":
			for _, c := range shim.SplitConfigList(v) {
				args = append(args, "--mcp-config", c)
			}
		case "mcp_config_file":
			for _, f := range shim.SplitConfigList(v) {
				args = append(args, "--mcp-config-file", f)
			}
		case "config_file":
			args = append(args, "--config-file", v)
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown kimi Config key: %s", k)})
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
