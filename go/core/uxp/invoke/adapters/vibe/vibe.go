// Package vibe implements the InvocationAdapter for Mistral Vibe CLI.
//
// Help-text basis: vibe 2.9.3 (2026-05-09 capture at
// .tlc/tracks/uxp-agent-cli-facade/help/vibe.txt).
//
// Distinctive shape:
//   - S-4 builtin-agent shim: --agent {default|plan|accept-edits|
//     auto-approve} doubles as approval mode. Caller-supplied
//     Invocation.Agent and Approval are mutually exclusive on vibe.
//   - --output text|json|streaming (not stream-json — different name).
//   - No --model flag (config-only).
//   - No --add-dir or per-file flag.
//   - --workdir <dir> for CWD (native).
package vibe

import (
	"errors"
	"fmt"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

const Binary = "vibe"

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) CLI() uxp.CLIName { return uxp.CLIVibe }

// approvalToAgent maps universal Approval values to vibe's --agent
// builtin names. Empty string = no override (default behavior).
func approvalToAgent(a invoke.ApprovalMode) string {
	switch a {
	case invoke.ApprovalPlan:
		return "plan"
	case invoke.ApprovalAutoEdit:
		return "accept-edits"
	case invoke.ApprovalAutoAll:
		return "auto-approve"
	}
	return ""
}

func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("vibe: Mode %q is not valid", inv.Mode)
	}
	if inv.Fork {
		return invoke.CommandSpec{}, ds, errors.New("vibe: Fork is unsupported")
	}
	if inv.Model != "" {
		return invoke.CommandSpec{}, ds,
			errors.New("vibe: Model is unsupported (vibe selects model via config; use --agent)")
	}
	if inv.Approval == invoke.ApprovalNever {
		return invoke.CommandSpec{}, ds,
			errors.New("vibe: ApprovalNever is unsupported")
	}

	args := []string{}

	switch inv.Mode {
	case invoke.ModeInteractive:
		// no flag
	case invoke.ModeRun:
		// vibe -p <text> goes at the end (composed prompt).
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("vibe: ModeResume requires SessionID or Continue=true")
		}
		if inv.Continue {
			args = append(args, "-c")
		} else {
			args = append(args, "--resume", inv.SessionID)
		}
	}

	// S-4: --agent slot is shared between caller-set Agent and
	// approval-mode shim. Refuse if both ask for it.
	approvalAgent := approvalToAgent(inv.Approval)
	if inv.Approval == invoke.ApprovalAutoAll && !configBool(inv.Config, "uxp.allow_dangerous") {
		ds.Add(shim.RefuseDangerousDegradation(
			"Approval", string(inv.Approval),
			"--agent auto-approve",
			[]string{"ApprovalAsk", "ApprovalAutoEdit", "ApprovalPlan"}))
		return invoke.CommandSpec{}, ds,
			errors.New("vibe: ApprovalAutoAll requires Config[\"uxp.allow_dangerous\"]=\"true\" (maps to --agent auto-approve)")
	}
	switch {
	case inv.Agent != "" && approvalAgent != "":
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"vibe: Agent=%q and Approval=%q both want --agent; pick one (S-4 shim)", inv.Agent, inv.Approval)
	case inv.Agent != "":
		args = append(args, "--agent", inv.Agent)
	case approvalAgent != "":
		args = append(args, "--agent", approvalAgent)
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Approval",
			Message: fmt.Sprintf("Approval=%q shimmed via --agent %s (S-4); caller-set Agent ignored", inv.Approval, approvalAgent)})
	}

	switch inv.Sandbox {
	case invoke.SandboxDefault:
		// nothing
	default:
		// vibe has no sandbox tier; auto-approve agent is the closest
		// peer to DangerFullAccess but it's also Approval territory.
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"vibe: Sandbox=%q is unsupported (no sandbox flag)", inv.Sandbox)
	}

	switch inv.Output {
	case invoke.OutputDefault, invoke.OutputText:
		// nothing
	case invoke.OutputJSON:
		args = append(args, "--output", "json")
	case invoke.OutputStreamJSON:
		args = append(args, "--output", "streaming")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("vibe: Output %q is not valid", inv.Output)
	}

	if inv.CWD != "" {
		args = append(args, "--workdir", inv.CWD)
	}

	// AddDirs / Files / Images: no native flags → S-3 prompt-block.
	if len(inv.AddDirs) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "AddDirs",
			Message: fmt.Sprintf("%d AddDirs listed in prompt block; vibe has no --add-dir flag", len(inv.AddDirs))})
	}
	if len(inv.Files) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Files",
			Message: fmt.Sprintf("%d Files listed in prompt block; vibe has no per-file flag", len(inv.Files))})
	}
	if len(inv.Images) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Images",
			Message: fmt.Sprintf("%d image(s) listed in prompt block; vibe has no headless image flag", len(inv.Images))})
	}

	args = appendConfigArgs(args, inv.Config, &ds)

	if len(inv.ExtraArgs) > 0 {
		args = append(args, inv.ExtraArgs...)
	}

	prompt := composePrompt(inv)
	switch inv.Mode {
	case invoke.ModeRun:
		args = append(args, "-p", prompt)
	default:
		if prompt != "" {
			args = append(args, prompt)
		}
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
	const ns = "vibe."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "max_turns":
			args = append(args, "--max-turns", v)
		case "max_price":
			args = append(args, "--max-price", v)
		case "enabled_tools":
			for _, t := range shim.SplitConfigList(v) {
				args = append(args, "--enabled-tools", t)
			}
		case "trust":
			if configBoolStr(v) {
				args = append(args, "--trust")
			}
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown vibe Config key: %s", k)})
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
