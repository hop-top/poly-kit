package cli

import (
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/sideeffect"
)

// Plan is the structured dry-run output of a write or destructive
// command. Agents pre-validate via dry-run, then re-issue without it
// for execution. See docs/adopters/reference/sideeffect.md.
//
// Adopters return a Plan from RunE when cli.IsDryRun(cmd) is true,
// printing it through output.RenderPlan so --format json/yaml
// produces a machine-parseable representation.
type Plan struct {
	// Command is the canonical command path (e.g. "kit alias add").
	Command string `json:"command" yaml:"command" table:"COMMAND"`
	// Args is the resolved (post-flag) argument map. Optional; some
	// commands have none.
	Args map[string]any `json:"args,omitempty" yaml:"args,omitempty"`
	// Effects is the ordered list of state changes the command would
	// apply if re-issued without --dry-run.
	Effects []Effect `json:"effects" yaml:"effects" table:"-"`
	// PrerequisitesChecked records the named pre-flight checks that
	// passed during planning (e.g. "auth", "config-loaded"). Optional.
	PrerequisitesChecked []string `json:"prerequisites_checked,omitempty" yaml:"prerequisites_checked,omitempty"`
	// Warnings carries non-fatal advisories the agent should surface
	// to the operator before execution. Optional.
	Warnings []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
	// GeneratedAt is when the plan was assembled. Stamped at return
	// time so re-issued plans differ; useful for audit trails.
	GeneratedAt time.Time `json:"generated_at" yaml:"generated_at"`
}

// Effect is one declared side-effect entry inside a Plan. Effects
// describe what the command WOULD do; they are not applied during
// dry-run.
type Effect struct {
	// Kind is a short verb describing the operation (e.g. "create",
	// "update", "delete").
	Kind string `json:"kind" yaml:"kind" table:"KIND"`
	// Target is the addressable resource the operation acts on
	// (e.g. "alias:foo", "/etc/hosts", "secret/db/prod").
	Target string `json:"target" yaml:"target" table:"TARGET"`
	// Reversible reports whether re-running with the inverse command
	// can restore prior state without data loss.
	Reversible bool `json:"reversible" yaml:"reversible" table:"REVERSIBLE"`
	// Detail is a free-form one-line note, shown in --format=table.
	Detail string `json:"detail,omitempty" yaml:"detail,omitempty" table:"DETAIL"`
}

// IsDryRun reports whether the command runs as a dry run: invoked
// with --dry-run, or its context tagged by kit (sideeffect.WithDryRun),
// which is how kit.dry_run from config or KIT_DRY_RUN arrives.
// Adopters call this in RunE; if true, build a Plan and return it via
// RenderPlan instead of executing.
func IsDryRun(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if v, err := cmd.Flags().GetBool("dry-run"); err == nil && v {
		return true
	}
	return sideeffect.IsDryRun(cmd.Context())
}

// RenderPlan writes p to cmd's stdout as the command's dry-run output,
// in the active --format: json and yaml serialize the Plan, any other
// format prints the plan table. An empty Command defaults to cmd's
// path and a zero GeneratedAt to now, so a leaf fills in only what it
// would change.
//
// A leaf honours --dry-run by returning RenderPlan(cmd, plan) before
// its first side effect:
//
//	if cli.IsDryRun(cmd) {
//		return cli.RenderPlan(cmd, cli.Plan{Effects: []cli.Effect{
//			{Kind: "delete", Target: "pattern:" + name},
//		}})
//	}
func RenderPlan(cmd *cobra.Command, p Plan) error {
	if p.Command == "" {
		p.Command = cmd.CommandPath()
	}
	if p.GeneratedAt.IsZero() {
		p.GeneratedAt = time.Now().UTC()
	}
	if p.Effects == nil {
		p.Effects = []Effect{}
	}
	format := output.Table
	switch f := activeFormat(cmd); f {
	case output.JSON, output.YAML:
		format = f
	}
	return output.RenderPlan(cmd.OutOrStdout(), format, &p)
}
