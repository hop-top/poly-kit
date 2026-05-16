// Package codex implements the InvocationAdapter for Codex CLI.
//
// Help-text basis: codex-cli 0.130.0 (2026-05-09 capture at
// .tlc/tracks/uxp-agent-cli-facade/help/codex.txt).
//
// Codex has four argv shapes for the universal Modes:
//   - ModeInteractive: codex [PROMPT]
//   - ModeRun:         codex exec [OPTIONS] [PROMPT]
//   - ModeResume:      codex exec resume [OPTIONS] [SESSION_ID|--last] [PROMPT]
//     (use codex resume for the interactive TUI flavor)
//   - ModeResume+Fork: codex fork [OPTIONS] [SESSION_ID|--last] [PROMPT]
//
// Sandbox is first-class: -s read-only|workspace-write|danger-full-access.
// Approval has no auto-edit; ApprovalAutoEdit is refused per anti-shim.
// ApprovalPlan is shimmed to "-s read-only -a never" (S-6).
package codex

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

const Binary = "codex"

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) CLI() uxp.CLIName { return uxp.CLICodex }

func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("codex: Mode %q is not valid", inv.Mode)
	}

	args := []string{}

	// Subcommand routing.
	switch inv.Mode {
	case invoke.ModeInteractive:
		// no subcommand
	case invoke.ModeRun:
		args = append(args, "exec")
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("codex: ModeResume requires SessionID or Continue=true")
		}
		// Codex distinguishes interactive resume (`codex resume`) from
		// headless resume (`codex exec resume`). Pick by signal: if the
		// caller asked for any structured output, they want headless;
		// otherwise default to the interactive flavor (matches typical
		// human resume + cross-CLI handoff use).
		headless := inv.Output != invoke.OutputDefault && inv.Output != invoke.OutputText
		switch {
		case inv.Fork:
			args = append(args, "fork")
		case headless:
			args = append(args, "exec", "resume")
		default:
			args = append(args, "resume")
		}
	}

	if inv.Mode != invoke.ModeResume && inv.Fork {
		return invoke.CommandSpec{}, ds,
			errors.New("codex: Fork=true requires ModeResume")
	}

	// Resume tail: --last or positional session id, plus optional
	// prompt added later. Note args ordering: subcommand is already
	// appended; --last is a flag of the subcommand.
	resumeFlags := []string{}
	if inv.Mode == invoke.ModeResume {
		if inv.Continue {
			resumeFlags = append(resumeFlags, "--last")
		}
	}

	// Output: only meaningful with exec (ModeRun).
	switch inv.Output {
	case invoke.OutputDefault, invoke.OutputText:
		// nothing
	case invoke.OutputJSON:
		// codex emits final message via -o <FILE>; needs a path.
		path := inv.Config["codex.output_last_message_path"]
		if path == "" {
			return invoke.CommandSpec{}, ds, errors.New(
				"codex: OutputJSON requires Config[\"codex.output_last_message_path\"]; codex writes the final message to a file, not stdout")
		}
		if inv.Mode == invoke.ModeInteractive {
			return invoke.CommandSpec{}, ds,
				errors.New("codex: OutputJSON requires ModeRun or ModeResume (exec subcommand)")
		}
		args = append(args, "-o", path)
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Output",
			Message: "codex OutputJSON shimmed via -o/--output-last-message <FILE>; final message is written to disk, not stdout"})
	case invoke.OutputStreamJSON:
		if inv.Mode == invoke.ModeInteractive {
			return invoke.CommandSpec{}, ds,
				errors.New("codex: OutputStreamJSON requires ModeRun or ModeResume")
		}
		args = append(args, "--json")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("codex: Output %q is not valid", inv.Output)
	}

	// Sandbox: native tiers.
	switch inv.Sandbox {
	case invoke.SandboxDefault:
		// nothing
	case invoke.SandboxReadOnly:
		args = append(args, "-s", "read-only")
	case invoke.SandboxWorkspaceWrite:
		args = append(args, "-s", "workspace-write")
	case invoke.SandboxDangerFullAccess:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Sandbox", string(inv.Sandbox),
				"-s danger-full-access",
				[]string{"SandboxWorkspaceWrite", "SandboxReadOnly"}))
			return invoke.CommandSpec{}, ds,
				errors.New("codex: SandboxDangerFullAccess is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
		}
		args = append(args, "-s", "danger-full-access")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("codex: Sandbox %q is not valid", inv.Sandbox)
	}

	// Approval: codex's policy (untrusted/on-request/never) plus the
	// dangerous bypass flag. ApprovalAutoEdit is refused (anti-shim).
	// ApprovalPlan is S-6: read-only sandbox + never approval.
	switch inv.Approval {
	case invoke.ApprovalDefault:
		// nothing
	case invoke.ApprovalAsk:
		args = append(args, "-a", "on-request")
	case invoke.ApprovalAutoEdit:
		ds.Add(shim.RefuseDangerousDegradation(
			"Approval", string(inv.Approval),
			"--dangerously-bypass-approvals-and-sandbox",
			[]string{"ApprovalAsk", "ApprovalNever"}))
		return invoke.CommandSpec{}, ds, errors.New(
			"codex: ApprovalAutoEdit is unsupported (no native auto-edit; refusing rather than degrade to dangerous bypass)")
	case invoke.ApprovalAutoAll:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Approval", string(inv.Approval),
				"--dangerously-bypass-approvals-and-sandbox",
				[]string{"ApprovalAsk", "ApprovalNever"}))
			return invoke.CommandSpec{}, ds,
				errors.New("codex: ApprovalAutoAll requires Config[\"uxp.allow_dangerous\"]=\"true\" (maps to --dangerously-bypass-approvals-and-sandbox)")
		}
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	case invoke.ApprovalPlan:
		// S-6 cross-shim: codex has no plan flag. Read-only sandbox
		// + never-approval is the safest peer.
		if !hasFlag(args, "-s") {
			args = append(args, "-s", "read-only")
		}
		args = append(args, "-a", "never")
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Approval",
			Message: "ApprovalPlan shimmed via -s read-only -a never (S-6); codex has no native plan mode"})
	case invoke.ApprovalNever:
		args = append(args, "-a", "never")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("codex: Approval %q is not valid", inv.Approval)
	}

	if inv.Model != "" {
		args = append(args, "-m", inv.Model)
	}
	if inv.Agent != "" {
		// codex has no --agent; use --profile via Config instead.
		return invoke.CommandSpec{}, ds, errors.New(
			"codex: Agent is unsupported; use Config[\"codex.profile\"] for config-profile selection")
	}

	if inv.CWD != "" {
		// codex has a real --cd flag. Apply it AND set Dir so callers
		// who consult either source see the same value.
		args = append(args, "-C", inv.CWD)
	}

	for _, d := range inv.AddDirs {
		args = append(args, "--add-dir", d)
	}

	// Files: no per-file flag → S-1 (parent-dir reduce → --add-dir)
	// + S-3 (prompt-block).
	if len(inv.Files) > 0 {
		parents := shim.ExpandToParentDirs(inv.Files)
		for _, p := range parents {
			if !slices.Contains(inv.AddDirs, p) {
				args = append(args, "--add-dir", p)
			}
		}
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Files",
			Message: fmt.Sprintf("Files reduced to %d parent dir(s) via --add-dir and listed in prompt block (S-1+S-3); codex has no per-file flag",
				len(parents))})
	}

	// Images: native -i/--image variadic.
	if len(inv.Images) > 0 {
		args = append(args, "-i")
		args = append(args, inv.Images...)
	}

	args = appendConfigArgs(args, inv.Config, &ds)

	// inv.Debug: codex has no -d flag; adopters pass debug via ExtraArgs.

	if len(inv.ExtraArgs) > 0 {
		args = append(args, inv.ExtraArgs...)
	}

	// Resume positional ordering: [SESSION_ID] [PROMPT]. The session
	// id (when provided without --last) goes right before the prompt.
	args = append(args, resumeFlags...)
	if inv.Mode == invoke.ModeResume && !inv.Continue && inv.SessionID != "" {
		args = append(args, inv.SessionID)
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
	b.WriteString(inv.Prompt)
	return b.String()
}

func appendConfigArgs(args []string, cfg map[string]string, ds *invoke.Diagnostics) []string {
	const ns = "codex."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "profile":
			args = append(args, "-p", v)
		case "config":
			// repeatable -c <key=value>
			for _, kv := range shim.SplitConfigList(v) {
				args = append(args, "-c", kv)
			}
		case "enable":
			for _, f := range shim.SplitConfigList(v) {
				args = append(args, "--enable", f)
			}
		case "disable":
			for _, f := range shim.SplitConfigList(v) {
				args = append(args, "--disable", f)
			}
		case "search":
			if configBoolStr(v) {
				args = append(args, "--search")
			}
		case "skip_git_repo_check":
			if configBoolStr(v) {
				args = append(args, "--skip-git-repo-check")
			}
		case "ephemeral":
			if configBoolStr(v) {
				args = append(args, "--ephemeral")
			}
		case "ignore_user_config":
			if configBoolStr(v) {
				args = append(args, "--ignore-user-config")
			}
		case "ignore_rules":
			if configBoolStr(v) {
				args = append(args, "--ignore-rules")
			}
		case "output_schema":
			args = append(args, "--output-schema", v)
		case "output_last_message_path":
			// already consumed above for OutputJSON; do not double-emit.
		case "oss":
			if configBoolStr(v) {
				args = append(args, "--oss")
			}
		case "local_provider":
			args = append(args, "--local-provider", v)
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown codex Config key: %s", k)})
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
