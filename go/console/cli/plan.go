package cli

import (
	"time"

	"github.com/spf13/cobra"
)

// Plan is the structured dry-run output of a write or destructive
// command. Agents pre-validate via dry-run, then re-issue without it
// for execution. See cli-conventions-with-kit.md §3.6.
//
// Adopters return a Plan from RunE when cli.IsDryRun(cmd) is true,
// printing it through output.RenderPlan so --format json/yaml
// produces a machine-parseable representation.
type Plan struct {
	// Command is the canonical command path (e.g. "kit alias add").
	Command string `json:"command" table:"COMMAND"`
	// Args is the resolved (post-flag) argument map. Optional; some
	// commands have none.
	Args map[string]any `json:"args,omitempty"`
	// Effects is the ordered list of state changes the command would
	// apply if re-issued without --dry-run.
	Effects []Effect `json:"effects" table:"-"`
	// PrerequisitesChecked records the named pre-flight checks that
	// passed during planning (e.g. "auth", "config-loaded"). Optional.
	PrerequisitesChecked []string `json:"prerequisites_checked,omitempty"`
	// Warnings carries non-fatal advisories the agent should surface
	// to the operator before execution. Optional.
	Warnings []string `json:"warnings,omitempty"`
	// GeneratedAt is when the plan was assembled. Stamped at return
	// time so re-issued plans differ; useful for audit trails.
	GeneratedAt time.Time `json:"generated_at"`
}

// Effect is one declared side-effect entry inside a Plan. Effects
// describe what the command WOULD do; they are not applied during
// dry-run.
type Effect struct {
	// Kind is a short verb describing the operation (e.g. "create",
	// "update", "delete").
	Kind string `json:"kind" table:"KIND"`
	// Target is the addressable resource the operation acts on
	// (e.g. "alias:foo", "/etc/hosts", "secret/db/prod").
	Target string `json:"target" table:"TARGET"`
	// Reversible reports whether re-running with the inverse command
	// can restore prior state without data loss.
	Reversible bool `json:"reversible" table:"REVERSIBLE"`
	// Detail is a free-form one-line note, shown in --format=table.
	Detail string `json:"detail,omitempty" table:"DETAIL"`
}

// IsDryRun reports whether the command was invoked with --dry-run.
// Adopters call this in RunE; if true, build a Plan and return it
// via output.RenderPlan instead of executing.
func IsDryRun(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if v, err := cmd.Flags().GetBool("dry-run"); err == nil {
		return v
	}
	return false
}
