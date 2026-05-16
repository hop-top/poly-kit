// Package copilot implements the InvocationAdapter for GitHub Copilot CLI.
//
// Help-text basis: GitHub Copilot CLI 1.0.15 (2026-05-09 capture at
// .tlc/tracks/uxp-agent-cli-facade/help/copilot.txt).
//
// Distinctive shape:
//   - run: copilot -p "<prompt>"
//   - interactive: copilot or copilot -i "<prompt>"
//   - resume: copilot --resume=<id> (or --continue for latest)
//   - no fork
//   - --output-format json is JSONL one-object-per-line; OutputJSON
//     is shimmed (caller must reduce); OutputStreamJSON is native
//   - non-interactive runs require --allow-all-tools or equivalent;
//     adapter warns when ModeRun is missing any auto-approve signal
package copilot

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

const Binary = "copilot"

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) CLI() uxp.CLIName { return uxp.CLICopilot }

func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("copilot: Mode %q is not valid", inv.Mode)
	}

	args := []string{}

	if inv.Fork {
		return invoke.CommandSpec{}, ds, errors.New("copilot: Fork is unsupported")
	}

	switch inv.Mode {
	case invoke.ModeInteractive:
		// no flag
	case invoke.ModeRun:
		// We add -p with the prompt at the end (after composing
		// file/image blocks).
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("copilot: ModeResume requires SessionID or Continue=true")
		}
		if inv.Continue {
			args = append(args, "--continue")
		} else {
			// copilot accepts --resume=<id> form.
			args = append(args, "--resume="+inv.SessionID)
		}
	}

	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	if inv.Agent != "" {
		args = append(args, "--agent", inv.Agent)
	}

	switch inv.Output {
	case invoke.OutputDefault, invoke.OutputText:
		// nothing
	case invoke.OutputJSON:
		args = append(args, "--output-format", "json")
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Output",
			Message: "copilot --output-format json is JSONL (one object per line); OutputJSON is shimmed — caller must reduce to final assistant message"})
	case invoke.OutputStreamJSON:
		args = append(args, "--output-format", "json")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("copilot: Output %q is not valid", inv.Output)
	}

	switch inv.Sandbox {
	case invoke.SandboxDefault:
		// nothing
	case invoke.SandboxReadOnly, invoke.SandboxWorkspaceWrite:
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"copilot: Sandbox=%q is unsupported (no per-tier sandbox; use --allow-tool/--deny-tool/--allow-url/--deny-url for tool policy)",
			inv.Sandbox)
	case invoke.SandboxDangerFullAccess:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Sandbox", string(inv.Sandbox),
				"--yolo (or --allow-all)",
				[]string{"SandboxDefault"}))
			return invoke.CommandSpec{}, ds, errors.New(
				"copilot: SandboxDangerFullAccess is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
		}
		args = append(args, "--yolo")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("copilot: Sandbox %q is not valid", inv.Sandbox)
	}

	switch inv.Approval {
	case invoke.ApprovalDefault, invoke.ApprovalAsk:
		// nothing
	case invoke.ApprovalAutoEdit:
		ds.Add(shim.RefuseDangerousDegradation(
			"Approval", string(inv.Approval),
			"--yolo (or --allow-all-tools)",
			[]string{"ApprovalAsk"}))
		return invoke.CommandSpec{}, ds, errors.New(
			"copilot: ApprovalAutoEdit is unsupported (no native auto-edit; refusing rather than degrade to dangerous bypass)")
	case invoke.ApprovalAutoAll:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Approval", string(inv.Approval),
				"--yolo",
				[]string{"ApprovalAsk"}))
			return invoke.CommandSpec{}, ds, errors.New(
				"copilot: ApprovalAutoAll requires Config[\"uxp.allow_dangerous\"]=\"true\" (maps to --yolo)")
		}
		if !slices.Contains(args, "--yolo") {
			args = append(args, "--yolo")
		}
	case invoke.ApprovalPlan, invoke.ApprovalNever:
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"copilot: Approval=%q is unsupported (no native plan / never modes)", inv.Approval)
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("copilot: Approval %q is not valid", inv.Approval)
	}

	// AddDirs: native --add-dir <directory> (repeatable).
	for _, d := range inv.AddDirs {
		args = append(args, "--add-dir", d)
	}

	// Files: no per-file flag → S-1 (parent-dir reduce → --add-dir) +
	// S-3 (prompt-block).
	if len(inv.Files) > 0 {
		parents := shim.ExpandToParentDirs(inv.Files)
		for _, p := range parents {
			if !slices.Contains(inv.AddDirs, p) {
				args = append(args, "--add-dir", p)
			}
		}
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Files",
			Message: fmt.Sprintf("Files reduced to %d parent dir(s) via --add-dir and listed in prompt block (S-1+S-3); copilot has no per-file flag",
				len(parents))})
	}

	if len(inv.Images) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Images",
			Message: fmt.Sprintf("%d image(s) listed in prompt block; copilot has no headless image flag",
				len(inv.Images))})
	}

	args = appendConfigArgs(args, inv.Config, &ds)

	if inv.Mode == invoke.ModeRun {
		// copilot needs an auto-approve flag in non-interactive mode.
		// If the caller did not provide one (via Approval=AutoAll,
		// Sandbox=Danger, or any Config allow_* key), warn that the
		// run will likely block on first tool prompt.
		if !hasAutoApproveSignal(args, inv.Config) {
			ds.Add(invoke.Diagnostic{Level: "warning", Option: "Approval",
				Message: "ModeRun without --allow-all-tools / --allow-all / --yolo / --allow-tool will likely block on first tool prompt; consider Config[\"copilot.allow_all_tools\"]=\"true\" or set Approval=ApprovalAutoAll with uxp.allow_dangerous"})
		}
	}

	if len(inv.ExtraArgs) > 0 {
		args = append(args, inv.ExtraArgs...)
	}

	prompt := composePrompt(inv)
	switch inv.Mode {
	case invoke.ModeRun:
		args = append(args, "-p", prompt)
	case invoke.ModeInteractive:
		if prompt != "" {
			args = append(args, "-i", prompt)
		}
	case invoke.ModeResume:
		// resume can take an optional follow-up via -p.
		if prompt != "" {
			args = append(args, "-p", prompt)
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

func hasAutoApproveSignal(args []string, cfg map[string]string) bool {
	signals := []string{"--yolo", "--allow-all", "--allow-all-tools"}
	for _, s := range signals {
		if slices.Contains(args, s) {
			return true
		}
	}
	for k := range cfg {
		switch strings.TrimPrefix(k, "copilot.") {
		case "allow_all", "allow_all_tools", "allow_all_paths", "allow_all_urls",
			"yolo", "allow_tool", "autopilot":
			if configBoolStr(cfg[k]) || cfg[k] != "" {
				return true
			}
		}
	}
	return false
}

func appendConfigArgs(args []string, cfg map[string]string, ds *invoke.Diagnostics) []string {
	const ns = "copilot."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "allow_tool":
			for _, t := range shim.SplitConfigList(v) {
				args = append(args, "--allow-tool="+t)
			}
		case "deny_tool":
			for _, t := range shim.SplitConfigList(v) {
				args = append(args, "--deny-tool="+t)
			}
		case "allow_url":
			for _, u := range shim.SplitConfigList(v) {
				args = append(args, "--allow-url="+u)
			}
		case "deny_url":
			for _, u := range shim.SplitConfigList(v) {
				args = append(args, "--deny-url="+u)
			}
		case "allow_all":
			if configBoolStr(v) {
				args = append(args, "--allow-all")
			}
		case "allow_all_tools":
			if configBoolStr(v) {
				args = append(args, "--allow-all-tools")
			}
		case "allow_all_paths":
			if configBoolStr(v) {
				args = append(args, "--allow-all-paths")
			}
		case "allow_all_urls":
			if configBoolStr(v) {
				args = append(args, "--allow-all-urls")
			}
		case "yolo":
			if configBoolStr(v) && !slices.Contains(args, "--yolo") {
				args = append(args, "--yolo")
			}
		case "available_tools":
			args = append(args, "--available-tools="+v)
		case "excluded_tools":
			args = append(args, "--excluded-tools="+v)
		case "autopilot":
			if configBoolStr(v) {
				args = append(args, "--autopilot")
			}
		case "max_autopilot_continues":
			args = append(args, "--max-autopilot-continues", v)
		case "share":
			if v == "true" || v == "1" {
				args = append(args, "--share")
			} else {
				args = append(args, "--share="+v)
			}
		case "share_gist":
			if configBoolStr(v) {
				args = append(args, "--share-gist")
			}
		case "effort":
			args = append(args, "--effort", v)
		case "no_ask_user":
			if configBoolStr(v) {
				args = append(args, "--no-ask-user")
			}
		case "no_custom_instructions":
			if configBoolStr(v) {
				args = append(args, "--no-custom-instructions")
			}
		case "stream":
			args = append(args, "--stream", v)
		case "log_level":
			args = append(args, "--log-level", v)
		case "secret_env_vars":
			args = append(args, "--secret-env-vars="+v)
		case "additional_mcp_config":
			args = append(args, "--additional-mcp-config", v)
		case "disable_mcp_server":
			for _, s := range shim.SplitConfigList(v) {
				args = append(args, "--disable-mcp-server", s)
			}
		case "disable_builtin_mcps":
			if configBoolStr(v) {
				args = append(args, "--disable-builtin-mcps")
			}
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown copilot Config key: %s", k)})
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
