// Package qwen implements the InvocationAdapter for Qwen Code CLI.
//
// Help-text basis: qwen 0.15.6 (2026-05-09 capture at
// .tlc/tracks/uxp-agent-cli-facade/help/qwen.txt).
//
// Qwen is the closest peer to gemini in shape but with full
// --approval-mode parity (plan|default|auto-edit|yolo). Native
// --include-directories/--add-dir alias for AddDirs. Output formats
// mirror gemini exactly. Resume by id via --resume <id>; Continue
// via -c/--continue. Positional prompt is the recommended form;
// -p/--prompt is documented as deprecated.
package qwen

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

const Binary = "qwen"

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) CLI() uxp.CLIName { return uxp.CLIQwen }

func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("qwen: Mode %q is not valid", inv.Mode)
	}
	if inv.Fork {
		return invoke.CommandSpec{}, ds, errors.New("qwen: Fork is unsupported")
	}
	if inv.Agent != "" {
		return invoke.CommandSpec{}, ds, errors.New("qwen: Agent is unsupported")
	}

	args := []string{}

	switch inv.Mode {
	case invoke.ModeInteractive:
		// no flag
	case invoke.ModeRun:
		// Positional prompt is the recommended form; -p is deprecated.
		// We do nothing here and append the prompt at the end.
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("qwen: ModeResume requires SessionID or Continue=true")
		}
		if inv.Continue {
			args = append(args, "-c")
		} else {
			args = append(args, "-r", inv.SessionID)
		}
	}

	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}

	switch inv.Output {
	case invoke.OutputDefault, invoke.OutputText:
		// nothing
	case invoke.OutputJSON:
		args = append(args, "--output-format", "json")
	case invoke.OutputStreamJSON:
		args = append(args, "--output-format", "stream-json")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("qwen: Output %q is not valid", inv.Output)
	}

	switch inv.Approval {
	case invoke.ApprovalDefault, invoke.ApprovalAsk:
		// nothing
	case invoke.ApprovalAutoEdit:
		args = append(args, "--approval-mode", "auto-edit")
	case invoke.ApprovalAutoAll:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Approval", string(inv.Approval),
				"--approval-mode yolo (or -y/--yolo)",
				[]string{"ApprovalAsk", "ApprovalAutoEdit"}))
			return invoke.CommandSpec{}, ds,
				errors.New("qwen: ApprovalAutoAll is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
		}
		args = append(args, "--approval-mode", "yolo")
	case invoke.ApprovalPlan:
		args = append(args, "--approval-mode", "plan")
	case invoke.ApprovalNever:
		return invoke.CommandSpec{}, ds, errors.New(
			"qwen: ApprovalNever is unsupported (no equivalent to claude's dontAsk)")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("qwen: Approval %q is not valid", inv.Approval)
	}

	switch inv.Sandbox {
	case invoke.SandboxDefault:
		// nothing
	case invoke.SandboxReadOnly:
		// shim via --approval-mode plan if not already set.
		if !hasFlag(args, "--approval-mode") {
			args = append(args, "--approval-mode", "plan")
		}
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Sandbox",
			Message: "SandboxReadOnly shimmed via --approval-mode plan; not a true sandbox"})
	case invoke.SandboxWorkspaceWrite:
		args = append(args, "--sandbox")
		ds.Add(invoke.Diagnostic{Level: "info", Option: "Sandbox",
			Message: "SandboxWorkspaceWrite mapped to qwen's boolean -s/--sandbox flag"})
	case invoke.SandboxDangerFullAccess:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Sandbox", string(inv.Sandbox),
				"-y/--yolo",
				[]string{"SandboxWorkspaceWrite", "SandboxReadOnly"}))
			return invoke.CommandSpec{}, ds, errors.New(
				"qwen: SandboxDangerFullAccess is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
		}
		args = append(args, "-y")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("qwen: Sandbox %q is not valid", inv.Sandbox)
	}

	// AddDirs: --include-directories (alias --add-dir) is array.
	for _, d := range inv.AddDirs {
		args = append(args, "--include-directories", d)
	}

	// Files: parent-dir reduce → --include-directories + S-3.
	if len(inv.Files) > 0 {
		parents := shim.ExpandToParentDirs(inv.Files)
		for _, p := range parents {
			if !slices.Contains(inv.AddDirs, p) {
				args = append(args, "--include-directories", p)
			}
		}
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Files",
			Message: fmt.Sprintf("Files reduced to %d parent dir(s) via --include-directories and listed in prompt block (S-1+S-3)", len(parents))})
	}

	if len(inv.Images) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Images",
			Message: fmt.Sprintf("%d image(s) listed in prompt block; qwen has no headless image flag", len(inv.Images))})
	}

	args = appendConfigArgs(args, inv.Config, &ds)

	if inv.Debug {
		args = append(args, "-d")
	}
	if len(inv.ExtraArgs) > 0 {
		args = append(args, inv.ExtraArgs...)
	}

	prompt := composePrompt(inv)
	if prompt != "" {
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
	const ns = "qwen."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "system_prompt":
			args = append(args, "--system-prompt", v)
		case "append_system_prompt":
			args = append(args, "--append-system-prompt", v)
		case "max_session_turns":
			args = append(args, "--max-session-turns", v)
		case "session_id":
			args = append(args, "--session-id", v)
		case "chat_recording":
			if configBoolStr(v) {
				args = append(args, "--chat-recording")
			}
		case "allowed_tools":
			for _, t := range shim.SplitConfigList(v) {
				args = append(args, "--allowed-tools", t)
			}
		case "allowed_mcp_server_names":
			for _, n := range shim.SplitConfigList(v) {
				args = append(args, "--allowed-mcp-server-names", n)
			}
		case "core_tools":
			for _, t := range shim.SplitConfigList(v) {
				args = append(args, "--core-tools", t)
			}
		case "exclude_tools":
			for _, t := range shim.SplitConfigList(v) {
				args = append(args, "--exclude-tools", t)
			}
		case "extensions":
			for _, e := range shim.SplitConfigList(v) {
				args = append(args, "--extensions", e)
			}
		case "auth_type":
			args = append(args, "--auth-type", v)
		case "channel":
			args = append(args, "--channel", v)
		case "openai_api_key":
			args = append(args, "--openai-api-key", v)
		case "openai_base_url":
			args = append(args, "--openai-base-url", v)
		case "screen_reader":
			if configBoolStr(v) {
				args = append(args, "--screen-reader")
			}
		case "bare":
			if configBoolStr(v) {
				args = append(args, "--bare")
			}
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown qwen Config key: %s", k)})
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
