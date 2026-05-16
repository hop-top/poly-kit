// Package opencode implements the InvocationAdapter for opencode CLI.
//
// Help-text basis: opencode 1.14.30 (2026-05-09 capture at
// .tlc/tracks/uxp-agent-cli-facade/help/opencode.txt).
//
// Distinctive shape:
//   - run: opencode run [message..]
//   - resume: opencode run --session <id> [message..] (or --continue)
//   - fork: --fork modifier with --continue or --session
//   - opencode is the only adapter where AddDirs has no native flag
//     but Files does. The S-2 shim (enumerateDirFiles) handles the
//     inversion: AddDirs → walk → repeated --file args.
package opencode

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/shim"
)

const Binary = "opencode"

// DefaultDirToFilesMax bounds AddDirs → S-2 expansion. Configurable
// via Invocation.Config["uxp.shim.dir_to_files_max"].
const DefaultDirToFilesMax = 200

type Adapter struct{}

func New() Adapter { return Adapter{} }

func (Adapter) CLI() uxp.CLIName { return uxp.CLIOpenCode }

func (Adapter) Build(inv invoke.Invocation) (invoke.CommandSpec, invoke.Diagnostics, error) {
	var ds invoke.Diagnostics

	if !inv.Mode.Valid() {
		return invoke.CommandSpec{}, ds, fmt.Errorf("opencode: Mode %q is not valid", inv.Mode)
	}

	args := []string{}

	switch inv.Mode {
	case invoke.ModeInteractive:
		// Bare `opencode [project]`. CWD goes via --dir or positional;
		// we use Dir on CommandSpec and let the binary infer.
	case invoke.ModeRun:
		args = append(args, "run")
	case invoke.ModeResume:
		if inv.SessionID == "" && !inv.Continue {
			return invoke.CommandSpec{}, ds,
				errors.New("opencode: ModeResume requires SessionID or Continue=true")
		}
		args = append(args, "run")
		if inv.Continue {
			args = append(args, "--continue")
		} else {
			args = append(args, "--session", inv.SessionID)
		}
		if inv.Fork {
			args = append(args, "--fork")
		}
	}

	if inv.Mode != invoke.ModeResume && inv.Fork {
		return invoke.CommandSpec{}, ds,
			errors.New("opencode: Fork=true requires ModeResume")
	}

	// CWD: --dir for run subcommand; CommandSpec.Dir always set.
	if inv.CWD != "" && inv.Mode == invoke.ModeRun {
		args = append(args, "--dir", inv.CWD)
	}
	if inv.CWD != "" && inv.Mode == invoke.ModeResume {
		args = append(args, "--dir", inv.CWD)
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
		// opencode --format json emits an event stream (JSONL). The
		// adapter records the shim; downstream callers must reduce
		// to the final assistant message themselves (or use
		// invoke/adapters/opencode/output for the helper, future
		// work).
		args = append(args, "--format", "json")
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "Output",
			Message: "opencode --format json is an event stream (JSONL); OutputJSON is shimmed — caller must reduce to final assistant message"})
	case invoke.OutputStreamJSON:
		args = append(args, "--format", "json")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("opencode: Output %q is not valid", inv.Output)
	}

	// Sandbox: opencode has no per-tier sandbox flag. Only the
	// dangerous bypass exists.
	switch inv.Sandbox {
	case invoke.SandboxDefault:
		// nothing
	case invoke.SandboxReadOnly, invoke.SandboxWorkspaceWrite:
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"opencode: Sandbox=%q is unsupported (no per-invocation sandbox tier; configure via opencode config)",
			inv.Sandbox)
	case invoke.SandboxDangerFullAccess:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Sandbox", string(inv.Sandbox),
				"--dangerously-skip-permissions",
				[]string{"SandboxDefault"}))
			return invoke.CommandSpec{}, ds, errors.New(
				"opencode: SandboxDangerFullAccess is dangerous; set Config[\"uxp.allow_dangerous\"]=\"true\" to opt in")
		}
		args = append(args, "--dangerously-skip-permissions")
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("opencode: Sandbox %q is not valid", inv.Sandbox)
	}

	// Approval: opencode has no per-tier approval. Only the dangerous
	// bypass approximates ApprovalAutoAll.
	switch inv.Approval {
	case invoke.ApprovalDefault, invoke.ApprovalAsk:
		// nothing
	case invoke.ApprovalAutoEdit:
		ds.Add(shim.RefuseDangerousDegradation(
			"Approval", string(inv.Approval),
			"--dangerously-skip-permissions",
			[]string{"ApprovalAsk"}))
		return invoke.CommandSpec{}, ds, errors.New(
			"opencode: ApprovalAutoEdit is unsupported (no native auto-edit; refusing rather than degrade to dangerous bypass)")
	case invoke.ApprovalAutoAll:
		if !configBool(inv.Config, "uxp.allow_dangerous") {
			ds.Add(shim.RefuseDangerousDegradation(
				"Approval", string(inv.Approval),
				"--dangerously-skip-permissions",
				[]string{"ApprovalAsk"}))
			return invoke.CommandSpec{}, ds, errors.New(
				"opencode: ApprovalAutoAll requires Config[\"uxp.allow_dangerous\"]=\"true\" (maps to --dangerously-skip-permissions)")
		}
		// Avoid double-emit if Sandbox already added it.
		if !slices.Contains(args, "--dangerously-skip-permissions") {
			args = append(args, "--dangerously-skip-permissions")
		}
	case invoke.ApprovalPlan, invoke.ApprovalNever:
		return invoke.CommandSpec{}, ds, fmt.Errorf(
			"opencode: Approval=%q is unsupported (no native plan / never modes)", inv.Approval)
	default:
		return invoke.CommandSpec{}, ds, fmt.Errorf("opencode: Approval %q is not valid", inv.Approval)
	}

	// Files: native --file (repeatable).
	for _, f := range inv.Files {
		args = append(args, "--file", f)
	}

	// AddDirs: opencode has no --add-dir. S-2 shim: enumerate dir →
	// repeated --file args. Honor uxp.shim.dir_to_files_max
	// (default 200); overflow → hard error per spec §15.5 S-2.
	if len(inv.AddDirs) > 0 {
		max := DefaultDirToFilesMax
		if v := inv.Config["uxp.shim.dir_to_files_max"]; v != "" {
			if n := atoiDefault(v, max); n > 0 {
				max = n
			}
		}
		// Filter: skip files we already passed via inv.Files to avoid
		// double-listing.
		seen := map[string]bool{}
		for _, f := range inv.Files {
			seen[f] = true
		}
		filter := func(p string) bool { return !seen[p] }

		totalAdded := 0
		for _, dir := range inv.AddDirs {
			files, overflow, err := shim.EnumerateDirFiles(dir, max-totalAdded, filter)
			if err != nil {
				return invoke.CommandSpec{}, ds, fmt.Errorf("opencode: AddDirs S-2 walk failed for %s: %w", dir, err)
			}
			if overflow {
				ds.Add(invoke.Diagnostic{Level: "error", Option: "AddDirs",
					Message: fmt.Sprintf("S-2 enumeration of %q overflowed cap %d; pass uxp.shim.dir_to_files_max=N to raise or split AddDirs", dir, max)})
				return invoke.CommandSpec{}, ds, fmt.Errorf(
					"opencode: AddDirs S-2 walk overflowed cap %d for dir %q", max, dir)
			}
			for _, f := range files {
				args = append(args, "--file", f)
				totalAdded++
			}
		}
		ds.Add(invoke.Diagnostic{Level: "warning", Option: "AddDirs",
			Message: fmt.Sprintf("AddDirs expanded via S-2 enumerateDirFiles to %d --file args (cap=%d); opencode has no --add-dir flag", totalAdded, max)})
	}

	if len(inv.Images) > 0 {
		// opencode --file accepts any path including images. Diagnose
		// the implicit reuse so the caller knows.
		for _, img := range inv.Images {
			args = append(args, "--file", img)
		}
		ds.Add(invoke.Diagnostic{Level: "info", Option: "Images",
			Message: fmt.Sprintf("%d image(s) passed via --file (opencode does not distinguish image attachments)", len(inv.Images))})
	}

	args = appendConfigArgs(args, inv.Config, &ds)

	if len(inv.ExtraArgs) > 0 {
		args = append(args, inv.ExtraArgs...)
	}

	// opencode run takes positional `message..`. The composed prompt
	// (Files prepended via S-3 not needed since --file is native;
	// keep the prompt clean).
	if inv.Prompt != "" {
		args = append(args, inv.Prompt)
	}

	return invoke.CommandSpec{
		Path: Binary,
		Args: args,
		Dir:  inv.CWD,
		Env:  inv.Env,
	}, ds, nil
}

func appendConfigArgs(args []string, cfg map[string]string, ds *invoke.Diagnostics) []string {
	const ns = "opencode."
	for k, v := range cfg {
		if !strings.HasPrefix(k, ns) {
			continue
		}
		switch strings.TrimPrefix(k, ns) {
		case "variant":
			args = append(args, "--variant", v)
		case "thinking":
			if configBoolStr(v) {
				args = append(args, "--thinking")
			}
		case "share":
			if configBoolStr(v) {
				args = append(args, "--share")
			}
		case "title":
			args = append(args, "--title", v)
		case "command":
			args = append(args, "--command", v)
		case "attach":
			args = append(args, "--attach", v)
		case "password":
			args = append(args, "--password", v)
		case "port":
			args = append(args, "--port", v)
		case "pure":
			if configBoolStr(v) {
				args = append(args, "--pure")
			}
		default:
			ds.Add(invoke.Diagnostic{Level: "info", Option: "Config",
				Message: fmt.Sprintf("unknown opencode Config key: %s", k)})
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

func atoiDefault(s string, def int) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	if n == 0 {
		return def
	}
	return n
}
