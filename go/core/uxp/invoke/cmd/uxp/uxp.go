// Package uxpcmd provides the `kit uxp` subcommand tree for building
// and inspecting agent CLI invocations across the eleven adapters in
// go/core/uxp/invoke/adapters/.
//
// The subcommand wires its public surface around invoke.Build (pure
// argv construction) and the per-adapter Mappings() / ToolCapabilities()
// data. Execution is opt-in: `kit uxp run` shells out via os/exec
// only after a successful Build; everything else (explain,
// capabilities, tools) is read-only.
package uxpcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/adapters/claude"
	"hop.top/kit/go/core/uxp/invoke/adapters/codex"
	"hop.top/kit/go/core/uxp/invoke/adapters/copilot"
	"hop.top/kit/go/core/uxp/invoke/adapters/crush"
	"hop.top/kit/go/core/uxp/invoke/adapters/cursoragent"
	"hop.top/kit/go/core/uxp/invoke/adapters/gemini"
	"hop.top/kit/go/core/uxp/invoke/adapters/goose"
	"hop.top/kit/go/core/uxp/invoke/adapters/kimi"
	"hop.top/kit/go/core/uxp/invoke/adapters/opencode"
	"hop.top/kit/go/core/uxp/invoke/adapters/qwen"
	"hop.top/kit/go/core/uxp/invoke/adapters/vibe"
)

// adapters returns a fresh map[CLIName]InvocationAdapter. Each call
// re-instantiates so adapters with future state are not shared
// across goroutines.
func adapters() map[uxp.CLIName]invoke.InvocationAdapter {
	all := []invoke.InvocationAdapter{
		claude.New(), gemini.New(), codex.New(), opencode.New(),
		copilot.New(), cursoragent.New(), qwen.New(),
		kimi.New(), vibe.New(), goose.New(), crush.New(),
	}
	m := make(map[uxp.CLIName]invoke.InvocationAdapter, len(all))
	for _, a := range all {
		m[a.CLI()] = a
	}
	return m
}

func adapterNames() []string {
	m := adapters()
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, string(n))
	}
	slices.Sort(names)
	return names
}

// Cmd returns the `kit uxp` subcommand tree.
func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uxp",
		Short: "Build and inspect agent CLI invocations across vendors",
		Long: "Build native argv for agent CLIs (claude, gemini, codex, " +
			"opencode, copilot, cursor-agent, qwen, kimi, vibe, goose, " +
			"crush) from one normalized request. Inspect per-option " +
			"parity, tool capabilities, and cross-CLI handoff via " +
			"explain / capabilities / tools subcommands.",
		Annotations: map[string]string{
			"kit/side-effect": "read",
			"kit/idempotent":  "yes",
		},
	}
	cmd.AddCommand(runCmd(), resumeCmd(), explainCmd(), capabilitiesCmd(), toolsCmd())
	// `kit uxp` is the depth-1 ancestor of `kit uxp tools map`; the
	// depth>=3 shape pass needs the chain marked hierarchical
	// unless the ancestor is reserved.
	kitcli.SetHierarchical(cmd)
	return cmd
}

// ---- shared flags ----

type commonFlags struct {
	tool        string
	model       string
	agent       string
	cwd         string
	format      string // text | json | stream-json
	approval    string
	sandbox     string
	files       []string
	images      []string
	addDirs     []string
	configKVs   []string
	extraArgs   []string
	allowDanger bool
	exec        bool
}

func bindCommon(cmd *cobra.Command, c *commonFlags) {
	cmd.Flags().StringVar(&c.tool, "tool", "", "target CLI (claude|gemini|codex|opencode|copilot|cursor-agent|qwen|kimi|vibe|goose|crush)")
	cmd.Flags().StringVar(&c.model, "model", "", "model name (vendor-specific)")
	cmd.Flags().StringVar(&c.agent, "agent", "", "agent / persona / recipe name (S-5 for goose; S-4 for vibe)")
	cmd.Flags().StringVar(&c.cwd, "cwd", "", "working directory for the spawned CLI")
	cmd.Flags().StringVar(&c.format, "format", "", "output format: text|json|stream-json (CLI-specific)")
	cmd.Flags().StringVar(&c.approval, "approval", "", "approval mode: ask|auto-edit|auto-all|plan|never")
	cmd.Flags().StringVar(&c.sandbox, "sandbox", "", "sandbox tier: read-only|workspace-write|danger-full-access")
	cmd.Flags().StringSliceVar(&c.files, "file", nil, "file to attach (repeatable)")
	cmd.Flags().StringSliceVar(&c.images, "image", nil, "image to attach (repeatable)")
	cmd.Flags().StringSliceVar(&c.addDirs, "add-dir", nil, "additional workspace directory (repeatable)")
	cmd.Flags().StringArrayVar(&c.configKVs, "config", nil, "Config key=value, namespaced as <cli>.<key> or uxp.<key> (repeatable; commas in values not split)")
	cmd.Flags().StringArrayVar(&c.extraArgs, "extra-arg", nil, "raw arg passed to the target CLI (repeatable)")
	cmd.Flags().BoolVar(&c.allowDanger, "allow-dangerous", false, "shorthand for --config uxp.allow_dangerous=true")
	cmd.Flags().BoolVar(&c.exec, "exec", false, "execute the built command instead of just printing argv")
}

func parseInvocation(c commonFlags, mode invoke.Mode, prompt string) (invoke.Invocation, error) {
	inv := invoke.Invocation{
		CLI:       uxp.CLIName(c.tool),
		Mode:      mode,
		Prompt:    prompt,
		Model:     c.model,
		Agent:     c.agent,
		CWD:       c.cwd,
		Files:     c.files,
		Images:    c.images,
		AddDirs:   c.addDirs,
		ExtraArgs: c.extraArgs,
	}

	if c.tool == "" {
		return inv, errors.New("--tool is required")
	}
	if _, ok := adapters()[uxp.CLIName(c.tool)]; !ok {
		return inv, fmt.Errorf("unknown --tool %q (known: %s)",
			c.tool, strings.Join(adapterNames(), ", "))
	}

	switch c.format {
	case "":
		inv.Output = invoke.OutputDefault
	case "text":
		inv.Output = invoke.OutputText
	case "json":
		inv.Output = invoke.OutputJSON
	case "stream-json":
		inv.Output = invoke.OutputStreamJSON
	default:
		return inv, fmt.Errorf("unknown --format %q (use text|json|stream-json)", c.format)
	}

	switch c.approval {
	case "":
	case "ask":
		inv.Approval = invoke.ApprovalAsk
	case "auto-edit":
		inv.Approval = invoke.ApprovalAutoEdit
	case "auto-all":
		inv.Approval = invoke.ApprovalAutoAll
	case "plan":
		inv.Approval = invoke.ApprovalPlan
	case "never":
		inv.Approval = invoke.ApprovalNever
	default:
		return inv, fmt.Errorf("unknown --approval %q", c.approval)
	}

	switch c.sandbox {
	case "":
	case "read-only":
		inv.Sandbox = invoke.SandboxReadOnly
	case "workspace-write":
		inv.Sandbox = invoke.SandboxWorkspaceWrite
	case "danger-full-access":
		inv.Sandbox = invoke.SandboxDangerFullAccess
	default:
		return inv, fmt.Errorf("unknown --sandbox %q", c.sandbox)
	}

	if len(c.configKVs) > 0 {
		inv.Config = make(map[string]string, len(c.configKVs))
		for _, kv := range c.configKVs {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return inv, fmt.Errorf("--config %q is not key=value", kv)
			}
			inv.Config[k] = v
		}
	}
	if c.allowDanger {
		if inv.Config == nil {
			inv.Config = map[string]string{}
		}
		inv.Config["uxp.allow_dangerous"] = "true"
	}

	return inv, nil
}

// ---- run ----

func runCmd() *cobra.Command {
	var c commonFlags
	cmd := &cobra.Command{
		Use:   "run [prompt]",
		Short: "Build (or execute) a one-shot run on the target CLI",
		Long: "Build native argv for a one-shot run on the target agent " +
			"CLI from the normalized request. Default prints the built " +
			"argv to stdout; --exec spawns the target CLI directly.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")
			inv, err := parseInvocation(c, invoke.ModeRun, prompt)
			if err != nil {
				return err
			}
			return buildAndMaybeExec(cmd, inv, c.exec)
		},
	}
	bindCommon(cmd, &c)
	// --exec is the gate: argv-print is read; spawned subprocess is
	// the only mutating path. Tag as write so the kit-global
	// --dry-run reaches RunE and the executable path can guard.
	kitcli.SetSideEffect(cmd, kitcli.SideEffectWrite)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyNo)
	return cmd
}

// ---- resume ----

func resumeCmd() *cobra.Command {
	var c commonFlags
	var session string
	var cont bool
	var fork bool
	cmd := &cobra.Command{
		Use:   "resume [prompt]",
		Short: "Resume a previous session on the target CLI",
		Long: "Build native argv to resume an existing session (or " +
			"continue the most recent one). Requires --session or " +
			"--continue; --fork forks the resumed session on adapters " +
			"that support it.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.Join(args, " ")
			inv, err := parseInvocation(c, invoke.ModeResume, prompt)
			if err != nil {
				return err
			}
			inv.SessionID = session
			inv.Continue = cont
			inv.Fork = fork
			if inv.SessionID == "" && !inv.Continue {
				return errors.New("--session <id> or --continue is required")
			}
			return buildAndMaybeExec(cmd, inv, c.exec)
		},
	}
	bindCommon(cmd, &c)
	cmd.Flags().StringVar(&session, "session", "", "session id to resume")
	cmd.Flags().BoolVar(&cont, "continue", false, "resume the most recent session")
	cmd.Flags().BoolVar(&fork, "fork", false, "fork the resumed session (only on adapters with native fork)")
	kitcli.SetSideEffect(cmd, kitcli.SideEffectWrite)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyNo)
	return cmd
}

// ---- explain ----

func explainCmd() *cobra.Command {
	var tool string
	var format string
	var option string
	cmd := &cobra.Command{
		Use:   "explain",
		Short: "Print the OptionMapping rows for a given target CLI",
		Long: "Show how each universal option maps onto the target CLI's " +
			"native flags. Useful for understanding what a Build will " +
			"emit before running it.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, ok := adapters()[uxp.CLIName(tool)]
			if !ok {
				return fmt.Errorf("unknown --tool %q (known: %s)",
					tool, strings.Join(adapterNames(), ", "))
			}
			mappings := a.Mappings()
			if option != "" {
				mappings = filterMappings(mappings, option)
				if len(mappings) == 0 {
					return fmt.Errorf("no mapping for option %q on %q", option, tool)
				}
			}
			return emitMappings(cmd.OutOrStdout(), tool, mappings, format)
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "", "target CLI (required)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text|json")
	cmd.Flags().StringVar(&option, "option", "", "filter to a specific universal option (e.g. ApprovalAutoEdit)")
	_ = cmd.MarkFlagRequired("tool")
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}

// ---- capabilities ----

func capabilitiesCmd() *cobra.Command {
	var tool string
	var format string
	cmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Dump Mappings + ToolCapabilities for one CLI",
		Long: "Print the full {Mappings, ToolCapabilities} surface of " +
			"the target agent CLI: every universal option's native " +
			"flag binding plus the per-tool permission and transcript " +
			"properties. Read-only.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, ok := adapters()[uxp.CLIName(tool)]
			if !ok {
				return fmt.Errorf("unknown --tool %q", tool)
			}
			payload := struct {
				CLI              uxp.CLIName             `json:"cli"`
				Mappings         []invoke.OptionMapping  `json:"mappings"`
				ToolCapabilities []invoke.ToolCapability `json:"tool_capabilities"`
			}{
				CLI:              a.CLI(),
				Mappings:         a.Mappings(),
				ToolCapabilities: a.ToolCapabilities(),
			}
			switch format {
			case "json":
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			case "text", "":
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "CLI: %s\n\nMappings:\n", payload.CLI)
				for _, m := range payload.Mappings {
					fmt.Fprintf(w, "  %-26s %-12s %s\n",
						m.Universal, m.Support, strings.Join(m.Native, ", "))
				}
				fmt.Fprintln(w, "\nToolCapabilities:")
				for _, c := range payload.ToolCapabilities {
					fmt.Fprintf(w, "  %-18s %-12s %s (transcript: %s)\n",
						c.Universal, c.Support, c.Permission, c.Transcript)
				}
				return nil
			default:
				return fmt.Errorf("unknown --format %q", format)
			}
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "", "target CLI (required)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text|json")
	_ = cmd.MarkFlagRequired("tool")
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}

// ---- tools (and tools map) ----

func toolsCmd() *cobra.Command {
	var tool string
	var format string
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Dump ToolCapabilities for one CLI",
		Long: "Print just the ToolCapabilities (per-tool support, " +
			"permission, transcript) for the target agent CLI. Pair " +
			"with `tools map` to compare two CLIs side-by-side.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, ok := adapters()[uxp.CLIName(tool)]
			if !ok {
				return fmt.Errorf("unknown --tool %q", tool)
			}
			caps := a.ToolCapabilities()
			switch format {
			case "json":
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(caps)
			case "text", "":
				for _, c := range caps {
					fmt.Fprintf(cmd.OutOrStdout(), "%-18s %-12s %s\n",
						c.Universal, c.Support, c.Permission)
				}
				return nil
			default:
				return fmt.Errorf("unknown --format %q", format)
			}
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "", "target CLI (required)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text|json")
	_ = cmd.MarkFlagRequired("tool")
	cmd.AddCommand(toolsMapCmd())
	// `uxp tools` is an intermediate node with both its own RunE and
	// a `map` sub-leaf below — mark hierarchical so the depth>=3
	// shape pass accepts `kit uxp tools map`.
	kitcli.SetHierarchical(cmd)
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}

func toolsMapCmd() *cobra.Command {
	var from, to, format string
	cmd := &cobra.Command{
		Use:   "map",
		Short: "Cross-CLI tool capability comparison (what survives a handoff?)",
		Long: "Compare ToolCapabilities between two agent CLIs to see " +
			"which tools survive a handoff (native|shim on both sides) " +
			"and which would be lost. Read-only.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			adps := adapters()
			a, ok := adps[uxp.CLIName(from)]
			if !ok {
				return fmt.Errorf("unknown --from %q", from)
			}
			b, ok := adps[uxp.CLIName(to)]
			if !ok {
				return fmt.Errorf("unknown --to %q", to)
			}
			fromCaps := indexCaps(a.ToolCapabilities())
			toCaps := indexCaps(b.ToolCapabilities())
			rows := []toolMapRow{}
			for universal, fc := range fromCaps {
				tc, ok := toCaps[universal]
				row := toolMapRow{Tool: universal, From: string(fc.Support)}
				if ok {
					row.To = string(tc.Support)
					row.Surviving = isCarrying(fc.Support) && isCarrying(tc.Support)
				} else {
					row.To = "missing"
				}
				rows = append(rows, row)
			}
			slices.SortFunc(rows, func(x, y toolMapRow) int {
				return strings.Compare(x.Tool, y.Tool)
			})
			switch format {
			case "json":
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"from": from, "to": to, "rows": rows,
				})
			default:
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Tool handoff %s → %s\n\n", from, to)
				fmt.Fprintf(w, "%-18s %-10s %-10s %s\n", "TOOL", "FROM", "TO", "SURVIVES")
				for _, r := range rows {
					mark := "yes"
					if !r.Surviving {
						mark = "no"
					}
					fmt.Fprintf(w, "%-18s %-10s %-10s %s\n", r.Tool, r.From, r.To, mark)
				}
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "source CLI (required)")
	cmd.Flags().StringVar(&to, "to", "", "target CLI (required)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text|json")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}

type toolMapRow struct {
	Tool      string `json:"tool"`
	From      string `json:"from_support"`
	To        string `json:"to_support"`
	Surviving bool   `json:"surviving"`
}

func indexCaps(caps []invoke.ToolCapability) map[string]invoke.ToolCapability {
	m := make(map[string]invoke.ToolCapability, len(caps))
	for _, c := range caps {
		m[c.Universal] = c
	}
	return m
}

// isCarrying reports whether a Support value means the capability is
// usable (Native or Shim). Unsupported and Dangerous do not carry —
// the latter because a cross-CLI handoff cannot rely on the caller
// having opted in on both sides.
func isCarrying(s invoke.MappingSupport) bool {
	return s == invoke.MappingNative || s == invoke.MappingShim
}

// ---- shared helpers ----

func filterMappings(ms []invoke.OptionMapping, option string) []invoke.OptionMapping {
	out := ms[:0:0]
	for _, m := range ms {
		if strings.EqualFold(m.Universal, option) {
			out = append(out, m)
		}
	}
	return out
}

func emitMappings(w io.Writer, tool string, mappings []invoke.OptionMapping, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"tool": tool, "mappings": mappings})
	case "text", "":
		fmt.Fprintf(w, "tool: %s\n", tool)
		for _, m := range mappings {
			fmt.Fprintf(w, "  %s [%s]\n", m.Universal, m.Support)
			if len(m.Native) > 0 {
				fmt.Fprintf(w, "    native: %s\n", strings.Join(m.Native, ", "))
			}
			if m.Notes != "" {
				fmt.Fprintf(w, "    notes:  %s\n", m.Notes)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown --format %q", format)
	}
}

func buildAndMaybeExec(cmd *cobra.Command, inv invoke.Invocation, doExec bool) error {
	a := adapters()[inv.CLI]
	spec, ds, err := a.Build(inv)
	w := cmd.OutOrStdout()

	// Always print diagnostics (info/warning) to stderr so stdout is
	// reserved for argv (--exec=false) or process output (--exec=true).
	for _, d := range ds {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s: %s\n", d.Level, d.Option, d.Message)
	}
	if err != nil {
		return err
	}

	if !doExec {
		// Print argv as one shell-quoted line, plus a newline-separated
		// breakdown for readability.
		fmt.Fprintln(w, shellEscape(append([]string{spec.Path}, spec.Args...)))
		return nil
	}

	osCmd := exec.Command(spec.Path, spec.Args...)
	osCmd.Dir = spec.Dir
	osCmd.Env = append(os.Environ(), spec.Env...)
	osCmd.Stdin = os.Stdin
	osCmd.Stdout = os.Stdout
	osCmd.Stderr = os.Stderr
	return osCmd.Run()
}

func shellEscape(parts []string) string {
	out := make([]string, len(parts))
	for i, p := range parts {
		if needsQuote(p) {
			out[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
		} else {
			out[i] = p
		}
	}
	return strings.Join(out, " ")
}

func needsQuote(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '"', '\'', '`', '$', '\\', '|', '&', ';', '<', '>', '(', ')', '[', ']', '{', '}', '*', '?', '~', '!', '#':
			return true
		}
	}
	return false
}
