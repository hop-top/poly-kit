package stage

import (
	"github.com/spf13/cobra"

	"hop.top/kit/go/core/stage"
	"hop.top/kit/go/runtime/domain"
)

// Config configures the shared stage subcommand tree.
//
// Required:
//   - ProjectResolver: returns the active scope identifier when the
//     user omits [scope] on stage show / stage why. Tools usually
//     wire this to their registered project ID.
//
// Optional:
//   - Publisher: domain.EventPublisher used by stage.Set / Propose
//     to emit transitioned/entered/proposed events. nil means no
//     bus emit (CLI still mutates projects.yaml).
//   - Topics: override the default kit.runtime.stage.* topics.
type Config struct {
	ProjectResolver func() string
	Publisher       domain.EventPublisher
	Topics          *stage.Topics
}

// New returns the top-level "stage" command with show/set/why/list
// attached.
//
// Adopters call once at boot:
//
//	rootCmd.AddCommand(stagecmd.New(stagecmd.Config{
//	    ProjectResolver: func() string { return cfg.Project.ID },
//	    Publisher:       myPublisher,
//	}))
//
// When Config.ProjectResolver is nil, [scope] is required on every
// subcommand that takes one (no fall-through).
func New(cfg Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stage",
		Short: "Inspect or change the operating mode of the active scope",
		Long: `Inspect or change the operating mode of a scope. Stage is one of:
  active            — normal mode (default)
  public_feedback   — pre-launch; only feedback-typed tracks may be created
  feature_freeze    — only fix/chore/docs work; no new tracks
  maintenance       — fix/chore/docs only; no new tracks
  sunset            — no creates; updates/deletes still ok
  archived          — read-only; all mutations blocked

Subcommands:
  show [scope]   print the current stage State for a scope
  set  <mode>    propose + persist a stage transition
  why  [scope]   list active stage rules
  list           print every scope's current stage`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(showCmd(cfg), setCmd(cfg), whyCmd(cfg), listCmd(cfg))
	return cmd
}

// resolveScope returns the user-supplied scope or the configured
// default. Returns "" when neither is available.
func resolveScope(args []string, cfg Config) string {
	if len(args) > 0 && args[0] != "" {
		return args[0]
	}
	if cfg.ProjectResolver != nil {
		return cfg.ProjectResolver()
	}
	return ""
}

// managerOpts builds the stage.Option slice from Config.
func managerOpts(cfg Config) []stage.Option {
	var opts []stage.Option
	if cfg.Publisher != nil {
		opts = append(opts, stage.WithPublisher(cfg.Publisher))
	}
	if cfg.Topics != nil {
		opts = append(opts, stage.WithTopics(*cfg.Topics))
	}
	return opts
}
