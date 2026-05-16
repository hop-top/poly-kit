// Package goose implements the InvocationAdapter for goose CLI (Block).
//
// Help-text basis: goose 1.33.1 (2026-05-09 capture at
// .tlc/tracks/uxp-agent-cli-facade/help/goose.txt; includes goose
// run + goose session subcommands).
//
// Distinctive shape:
//   - Subcommand routing: goose run / goose session.
//   - Native fork via `goose session --resume --fork`.
//   - S-5 recipe shim: Agent → --recipe <name>; richer recipe params
//     via Config keys (goose.recipe_params, goose.sub_recipe).
//   - Full --output-format text|json|stream-json parity.
//   - --provider override via Config (goose.provider).
//   - No CWD flag (process cwd via Dir).
//   - No per-invocation sandbox or approval mode (config-only).
package goose

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

const Binary = "goose"

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) CLI() uxp.CLIName { return uxp.CLIGoose }

func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("goose: Mode %q is not valid", inv.Mode)
	}

	args := []string{}

	// Subcommand routing.
	switch inv.Mode {
	case invoke.ModeInteractive:
		args = append(args, "session")
	case invoke.ModeRun:
		args = append(args, "run")
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("goose: ModeResume requires SessionID or Continue=true")
		}
		// Forking requires `goose session --resume --fork`.
		// Plain headless resume goes through `goose run --resume`.
		if inv.Fork {
			args = append(args, "session", "--resume", "--fork")
		} else {
			args = append(args, "run", "--resume")
		}
		if inv.SessionID != "" {
			args = append(args, "--session-id", inv.SessionID)
		}
	}

	if inv.Mode != invoke.ModeResume && inv.Fork {
		return invoke.CommandSpec{}, ds,
			errors.New("goose: Fork=true requires ModeResume")
	}

	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}

	// S-5: Agent → --recipe shim.
	if inv.Agent != "" {
		args = append(args, "--recipe", inv.Agent)
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Agent",
			Message: "Agent shimmed via --recipe (S-5); goose recipes are richer than agents — set Config[\"goose.recipe_params\"] for parameters and Config[\"goose.sub_recipe\"] for sub-recipes"})
	}

	switch inv.Output {
	case invoke.OutputDefault, invoke.OutputText:
		// nothing
	case invoke.OutputJSON:
		args = append(args, "--output-format", "json")
	case invoke.OutputStreamJSON:
		args = append(args, "--output-format", "stream-json")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("goose: Output %q is not valid", inv.Output)
	}

	// Sandbox / Approval: goose configures these globally, not per
	// invocation. Refuse all non-default values.
	if inv.Sandbox != invoke.SandboxDefault {
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"goose: Sandbox=%q is unsupported (configured globally via goose configure)", inv.Sandbox)
	}
	if inv.Approval != invoke.ApprovalDefault && inv.Approval != invoke.ApprovalAsk {
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"goose: Approval=%q is unsupported (configured globally via goose configure)", inv.Approval)
	}

	// AddDirs / Files / Images: goose has no native flags. S-3 prompt
	// block for all three.
	if len(inv.AddDirs) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "AddDirs",
			Message: fmt.Sprintf("%d AddDirs listed in prompt block; goose has no --add-dir flag", len(inv.AddDirs))})
	}
	if len(inv.Files) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Files",
			Message: fmt.Sprintf("%d Files listed in prompt block; goose has no per-file flag", len(inv.Files))})
	}
	if len(inv.Images) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Images",
			Message: fmt.Sprintf("%d image(s) listed in prompt block; goose has no headless image flag", len(inv.Images))})
	}

	args = appendConfigArgs(args, inv.Config, &ds)

	if inv.Debug {
		args = append(args, "--debug")
	}
	if len(inv.ExtraArgs) > 0 {
		args = append(args, inv.ExtraArgs...)
	}

	prompt := composePrompt(inv)
	if prompt != "" {
		// goose run takes -t <text>; goose session does not accept
		// inline prompt text via flag (interactive only).
		switch {
		case inv.Mode == invoke.ModeRun:
			args = append(args, "-t", prompt)
		case inv.Mode == invoke.ModeResume && !inv.Fork:
			// Headless resume via `goose run --resume`; -t for
			// follow-up prompt.
			args = append(args, "-t", prompt)
		}
		// In ModeInteractive or fork-via-session, no inline prompt.
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
	const ns = "goose."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "provider":
			args = append(args, "--provider", v)
		case "name":
			args = append(args, "--name", v)
		case "session_id":
			// Only emit if not already present from resume routing.
			if !slices.Contains(args, "--session-id") {
				args = append(args, "--session-id", v)
			}
		case "system":
			args = append(args, "--system", v)
		case "max_turns":
			args = append(args, "--max-turns", v)
		case "max_tool_repetitions":
			args = append(args, "--max-tool-repetitions", v)
		case "container":
			args = append(args, "--container", v)
		case "with_extension":
			for _, e := range shim.SplitConfigList(v) {
				args = append(args, "--with-extension", e)
			}
		case "with_streamable_http_extension":
			for _, e := range shim.SplitConfigList(v) {
				args = append(args, "--with-streamable-http-extension", e)
			}
		case "with_builtin":
			args = append(args, "--with-builtin", v)
		case "no_profile":
			if configBoolStr(v) {
				args = append(args, "--no-profile")
			}
		case "no_session":
			if configBoolStr(v) {
				args = append(args, "--no-session")
			}
		case "quiet":
			if configBoolStr(v) {
				args = append(args, "--quiet")
			}
		case "recipe_params":
			for _, p := range shim.SplitConfigList(v) {
				args = append(args, "--params", p)
			}
		case "sub_recipe":
			for _, r := range shim.SplitConfigList(v) {
				args = append(args, "--sub-recipe", r)
			}
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown goose Config key: %s", k)})
		}
	}
	return args
}

func configBoolStr(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
