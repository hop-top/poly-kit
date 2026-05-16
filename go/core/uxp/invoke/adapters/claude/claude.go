// Package claude implements the InvocationAdapter for Claude Code.
//
// Help-text basis: claude --version 2.1.118 (2026-05-09 capture at
// .tlc/tracks/uxp-agent-cli-facade/help/claude.txt).
package claude

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

// Binary is the binary name. Adapters do not resolve absolute paths;
// the runner is expected to use $PATH.
const Binary = "claude"

// Adapter is the Claude Code invocation adapter.
type Adapter struct{}

// New returns a fresh Adapter. Stateless; safe to share.
func New() Adapter { return Adapter{} }

// CLI implements invoke.InvocationAdapter.
func (Adapter) CLI() uxp.CLIName { return uxp.CLIClaude }

// Build implements invoke.InvocationAdapter.
//
// Returned Diagnostics carry shim notes and unknown-Config-key info.
// A non-nil error is reserved for impossible inputs (missing required
// fields) and refused mappings (anti-shim policy in spec §15.5).
func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("claude: Mode %q is not valid", inv.Mode)
	}

	args := []string{}

	switch inv.Mode {
	case invoke.ModeInteractive:
		// Positional prompt is optional in interactive mode; argv just
		// adds prompt at the end if present.
	case invoke.ModeRun:
		args = append(args, "-p")
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("claude: ModeResume requires SessionID or Continue=true")
		}
		// claude's resume can be either interactive (TUI) or headless
		// (-p print mode). Pick by signal: an explicit non-text Output
		// implies the caller wants the headless print path.
		if inv.Output != invoke.OutputDefault && inv.Output != invoke.OutputText {
			args = append(args, "-p")
		}
		if inv.Continue {
			args = append(args, "--continue")
		} else {
			args = append(args, "--resume", inv.SessionID)
		}
		if inv.Fork {
			args = append(args, "--fork-session")
		}
	}

	if inv.Mode != invoke.ModeResume && inv.Fork {
		return invoke.CommandSpec{}, ds,
			errors.New("claude: Fork=true requires ModeResume")
	}

	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	if inv.Agent != "" {
		args = append(args, "--agent", inv.Agent)
	}

	// Output format. OutputDefault is text; emit only when the caller
	// asked for a specific shape. Streaming/JSON modes only work with
	// -p per the help text — refuse them in interactive mode.
	switch inv.Output {
	case invoke.OutputDefault, invoke.OutputText:
		// nothing
	case invoke.OutputJSON, invoke.OutputStreamJSON:
		if inv.Mode == invoke.ModeInteractive {
			return invoke.CommandSpec{}, ds, fmt.Errorf(
				"claude: Output=%q requires --print (ModeRun or ModeResume)", inv.Output)
		}
		switch inv.Output {
		case invoke.OutputJSON:
			args = append(args, "--output-format", "json")
		case invoke.OutputStreamJSON:
			args = append(args, "--output-format", "stream-json")
		}
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("claude: Output %q is not valid", inv.Output)
	}

	// Approval. Anti-shim: ApprovalAutoEdit is native; the rejection
	// rule in §15.5 applies to CLIs *without* it. claude has it.
	switch inv.Approval {
	case invoke.ApprovalDefault, invoke.ApprovalAsk:
		// claude defaults to "default" — leave alone.
	case invoke.ApprovalAutoEdit:
		args = append(args, "--permission-mode", "acceptEdits")
	case invoke.ApprovalAutoAll:
		// Dangerous: requires explicit caller opt-in.
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Approval", string(inv.Approval),
				"--permission-mode bypassPermissions",
				[]string{"ApprovalAsk", "ApprovalAutoEdit"}))
			return invoke.CommandSpec{}, ds,
				errors.New("claude: ApprovalAutoAll is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
		}
		args = append(args, "--permission-mode", "bypassPermissions")
	case invoke.ApprovalPlan:
		args = append(args, "--permission-mode", "plan")
	case invoke.ApprovalNever:
		// claude's "dontAsk" is the closest peer.
		args = append(args, "--permission-mode", "dontAsk")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("claude: Approval %q is not valid", inv.Approval)
	}

	// Sandbox. claude has no first-class sandbox tier; map onto
	// permission-mode shims per §15.4.
	switch inv.Sandbox {
	case invoke.SandboxDefault:
		// nothing
	case invoke.SandboxReadOnly:
		// Plan mode is read-only by convention; if Approval also set
		// to plan we already added the flag — avoid duplicating.
		if !hasFlag(args, "--permission-mode") {
			args = append(args, "--permission-mode", "plan")
		}
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Sandbox",
			Message: "SandboxReadOnly shimmed via --permission-mode plan; not a true sandbox"})
	case invoke.SandboxWorkspaceWrite:
		// claude default behavior is workspace-write via tool policy;
		// emit informational diagnostic only.
		ds.Add(invoke.Diagnostic{Level: "info", Option: "Sandbox",
			Message: "SandboxWorkspaceWrite is the default; no explicit flag needed"})
	case invoke.SandboxDangerFullAccess:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Sandbox", string(inv.Sandbox),
				"--dangerously-skip-permissions",
				[]string{"SandboxWorkspaceWrite", "SandboxReadOnly"}))
			return invoke.CommandSpec{}, ds,
				errors.New("claude: SandboxDangerFullAccess is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
		}
		args = append(args, "--dangerously-skip-permissions")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("claude: Sandbox %q is not valid", inv.Sandbox)
	}

	// AddDirs are native (variadic).
	if len(inv.AddDirs) > 0 {
		args = append(args, "--add-dir")
		args = append(args, inv.AddDirs...)
	}

	// Files: claude has no headless local-file flag (--file is for
	// downloaded resources, not local). Apply S-1 + S-3.
	if len(inv.Files) > 0 {
		extraDirs := shim.ExpandToParentDirs(inv.Files)
		if len(extraDirs) > 0 {
			if !hasFlag(args, "--add-dir") {
				args = append(args, "--add-dir")
			}
			args = append(args, extraDirs...)
		}
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Files",
			Message: fmt.Sprintf("Files reduced to %d parent dir(s) and listed in prompt block; claude has no local --file flag",
				len(extraDirs))})
	}

	// Images: no headless image flag → prompt block (S-3) only.
	if len(inv.Images) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Images",
			Message: fmt.Sprintf("%d image(s) listed in prompt block; claude has no headless image flag",
				len(inv.Images))})
	}

	// Compose the prompt: prepend file/image blocks if present.
	prompt := composePrompt(inv)

	// Per-CLI Config keys.
	args = appendConfigArgs(args, inv.Config, &ds)

	if inv.Debug {
		args = append(args, "-d")
	}

	if len(inv.ExtraArgs) > 0 {
		args = append(args, inv.ExtraArgs...)
	}

	if prompt != "" && inv.Mode != invoke.ModeResume {
		args = append(args, prompt)
	} else if prompt != "" && inv.Mode == invoke.ModeResume {
		// Resume with a follow-up prompt — claude accepts a
		// positional prompt alongside --resume/--continue.
		args = append(args, prompt)
	}

	return invoke.CommandSpec{
		Path: Binary,
		Args: args,
		Dir:  inv.CWD,
		Env:  inv.Env,
	}, ds, nil
}

// composePrompt prepends a file-block (S-3) and image-block to the
// caller's prompt when those slices are non-empty.
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

// appendConfigArgs interprets known claude.* Config keys and emits
// info diagnostics for unknown keys in the claude namespace.
func appendConfigArgs(args []string, cfg map[string]string, ds *invoke.Diagnostics) []string {
	const ns = "claude."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "system_prompt":
			args = append(args, "--system-prompt", v)
		case "append_system_prompt":
			args = append(args, "--append-system-prompt", v)
		case "allowed_tools":
			for _, t := range shim.SplitConfigList(v) {
				args = append(args, "--allowedTools", t)
			}
		case "disallowed_tools":
			for _, t := range shim.SplitConfigList(v) {
				args = append(args, "--disallowedTools", t)
			}
		case "tools":
			args = append(args, "--tools", v)
		case "settings":
			args = append(args, "--settings", v)
		case "max_budget_usd":
			args = append(args, "--max-budget-usd", v)
		case "session_id":
			args = append(args, "--session-id", v)
		case "fallback_model":
			args = append(args, "--fallback-model", v)
		case "effort":
			args = append(args, "--effort", v)
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown claude Config key: %s", k)})
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
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}
