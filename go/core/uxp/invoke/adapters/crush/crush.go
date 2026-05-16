// Package crush implements the InvocationAdapter for Crush CLI
// (Charmbracelet).
//
// Help-text basis: crush v0.65.2 (2026-05-09 capture at
// .tlc/tracks/uxp-agent-cli-facade/help/crush.txt).
//
// Distinctive shape:
//   - run: crush run [prompt...]
//   - interactive: crush
//   - resume by id: crush run --session <id>
//   - resume latest: crush run --continue
//   - no fork
//   - --cwd / -c (top-level flag, applies everywhere)
//   - --yolo / -y is auto-all only
//   - NO output-format flag → OutputJSON and OutputStreamJSON both
//     refused
//   - NO add-dir / no per-file flag
package crush

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

const Binary = "crush"

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) CLI() uxp.CLIName { return uxp.CLICrush }

func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("crush: Mode %q is not valid", inv.Mode)
	}
	if inv.Fork {
		return invoke.CommandSpec{}, ds, errors.New("crush: Fork is unsupported")
	}
	if inv.Agent != "" {
		return invoke.CommandSpec{}, ds, errors.New("crush: Agent is unsupported")
	}

	args := []string{}

	switch inv.Mode {
	case invoke.ModeInteractive:
		// no subcommand — bare `crush`
	case invoke.ModeRun:
		args = append(args, "run")
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("crush: ModeResume requires SessionID or Continue=true")
		}
		args = append(args, "run")
		if inv.Continue {
			args = append(args, "--continue")
		} else {
			args = append(args, "--session", inv.SessionID)
		}
	}

	if inv.CWD != "" {
		args = append(args, "--cwd", inv.CWD)
	}
	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}

	switch inv.Output {
	case invoke.OutputDefault, invoke.OutputText:
		// nothing
	case invoke.OutputJSON, invoke.OutputStreamJSON:
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"crush: Output=%q is unsupported (no --format flag exists)", inv.Output)
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("crush: Output %q is not valid", inv.Output)
	}

	if inv.Sandbox != invoke.SandboxDefault {
		switch inv.Sandbox {
		case invoke.SandboxDangerFullAccess:
			if !configBool(inv.Config, "uxp.allow_dangerous") {
				ds.Add(shim.RefuseDangerousDegradation(
					"Sandbox", string(inv.Sandbox),
					"--yolo",
					[]string{"SandboxDefault"}))
				return invoke.CommandSpec{}, ds, errors.New(
					"crush: SandboxDangerFullAccess is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
			}
			args = append(args, "--yolo")
		default:
			return invoke.CommandSpec{}, ds, fmt.Errorf(
				"crush: Sandbox=%q is unsupported", inv.Sandbox)
		}
	}

	switch inv.Approval {
	case invoke.ApprovalDefault, invoke.ApprovalAsk:
		// nothing
	case invoke.ApprovalAutoEdit:
		ds.Add(shim.RefuseDangerousDegradation(
			"Approval", string(inv.Approval),
			"--yolo",
			[]string{"ApprovalAsk"}))
		return invoke.CommandSpec{}, ds, errors.New(
			"crush: ApprovalAutoEdit is unsupported (--yolo is auto-all only)")
	case invoke.ApprovalAutoAll:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Approval", string(inv.Approval),
				"--yolo",
				[]string{"ApprovalAsk"}))
			return invoke.CommandSpec{}, ds, errors.New(
				"crush: ApprovalAutoAll requires Config[\"uxp.allow_dangerous\"]=\"true\" (maps to --yolo)")
		}
		if !slices.Contains(args, "--yolo") {
			args = append(args, "--yolo")
		}
	case invoke.ApprovalPlan, invoke.ApprovalNever:
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"crush: Approval=%q is unsupported", inv.Approval)
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("crush: Approval %q is not valid", inv.Approval)
	}

	if len(inv.AddDirs) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "AddDirs",
			Message: fmt.Sprintf("%d AddDirs listed in prompt block; crush has no --add-dir flag", len(inv.AddDirs))})
	}
	if len(inv.Files) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Files",
			Message: fmt.Sprintf("%d Files listed in prompt block; crush has no per-file flag", len(inv.Files))})
	}
	if len(inv.Images) > 0 {
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Images",
			Message: fmt.Sprintf("%d image(s) listed in prompt block; crush has no headless image flag", len(inv.Images))})
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
	const ns = "crush."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "small_model":
			args = append(args, "--small-model", v)
		case "data_dir":
			args = append(args, "--data-dir", v)
		case "host":
			args = append(args, "--host", v)
		case "quiet":
			if configBoolStr(v) {
				args = append(args, "--quiet")
			}
		case "verbose":
			if configBoolStr(v) {
				args = append(args, "--verbose")
			}
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown crush Config key: %s", k)})
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
