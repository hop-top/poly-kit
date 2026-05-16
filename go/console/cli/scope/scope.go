// Package scope provides the "kit scope" CLI subcommand tree for inspecting
// path policy: show effective rules, check a single (path, op) pair, or
// bulk-test a list of paths.
//
// Subcommands:
//
//	kit scope show   [--tool <name>]
//	kit scope check  <path> [--op read|write|exec] [--tool <name>]
//	kit scope test   <path>... [--op ...] [--tool <name>]
//
// All commands honor --format table|json|yaml via go/console/output.
// Exit codes: 0 allowed, 1 denied (or any deny in a bulk run), 2 usage error.
package scope

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	scopepkg "hop.top/kit/go/core/scope"
)

// Cmd returns the top-level "scope" command with all subcommands attached.
func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scope",
		Short: "Inspect path policy guardrails",
		Long: `Inspect the kit/scope policy: print the effective allow/deny rules,
check a single (path, op) decision, or bulk-test multiple paths.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(showCmd(), checkCmd(), testCmd())
	return cmd
}

// resolvePolicy returns the policy to use: scope.FromConfig(tool) when
// --tool is provided, else scope.Default().
func resolvePolicy(tool string) (*scopepkg.Policy, error) {
	if tool == "" {
		return scopepkg.Default(), nil
	}
	return scopepkg.FromConfig(tool)
}

// parseOp converts the --op flag to a scope.Op bitset. Empty defaults to Read.
func parseOp(s string) (scopepkg.Op, error) {
	if s == "" {
		return scopepkg.Read, nil
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "read", "r":
		return scopepkg.Read, nil
	case "write", "w":
		return scopepkg.Write, nil
	case "exec", "x":
		return scopepkg.Exec, nil
	default:
		return 0, fmt.Errorf("unknown op %q (want read|write|exec)", s)
	}
}

// modeName returns the human label for a scope.Mode.
func modeName(m scopepkg.Mode) string {
	switch m {
	case scopepkg.Strict:
		return "strict"
	case scopepkg.Warn:
		return "warn"
	case scopepkg.Prompt:
		return "prompt"
	default:
		return "unknown"
	}
}

// decisionName returns the human label for a scope.Decision.
func decisionName(d scopepkg.Decision) string {
	switch d {
	case scopepkg.Allowed:
		return "allowed"
	case scopepkg.Denied:
		return "denied"
	default:
		return "unknown"
	}
}

// opLabel returns a human label for an Op bitset.
func opLabel(op scopepkg.Op) string {
	parts := make([]string, 0, 3)
	if op&scopepkg.Read != 0 {
		parts = append(parts, "read")
	}
	if op&scopepkg.Write != 0 {
		parts = append(parts, "write")
	}
	if op&scopepkg.Exec != 0 {
		parts = append(parts, "exec")
	}
	return strings.Join(parts, "|")
}
