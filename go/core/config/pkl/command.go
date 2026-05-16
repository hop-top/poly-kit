package pkl

import (
	"fmt"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/wizard"
	"hop.top/kit/go/core/config"
)

// CommandOpts configures the cobra command created by NewConfigCommand.
type CommandOpts struct {
	ConfigOpts config.Options
	Scope      config.Scope
	WizardOpts []wizard.RunOption
}

// NewConfigCommand creates a cobra.Command that runs a PKL-driven
// config wizard. Use as: root.AddCommand(pkl.NewConfigCommand(...))
func NewConfigCommand(pklPath string, opts CommandOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration interactively",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			answersFile, _ := cmd.Flags().GetString("answers-file")
			scopeStr, _ := cmd.Flags().GetString("scope")

			scope := opts.Scope
			if cmd.Flags().Changed("scope") {
				var err error
				scope, err = parseScope(scopeStr)
				if err != nil {
					return err
				}
			}

			wizOpts := WizardOpts{
				ConfigOpts: opts.ConfigOpts,
				Scope:      scope,
				DryRun:     dryRun,
				WizardOpts: opts.WizardOpts,
			}

			if answersFile != "" {
				answers, err := wizard.LoadAnswers(answersFile)
				if err != nil {
					return fmt.Errorf("load answers: %w", err)
				}
				wizOpts.Headless = answers
			}

			return RunWizard(cmd.Context(), pklPath, wizOpts)
		},
	}

	cmd.Flags().Bool("dry-run", false, "preview without writing config")
	cmd.Flags().String("answers-file", "", "path to YAML answers file")
	cmd.Flags().String("scope", "project", "config scope (system|user|project)")

	return cmd
}

// parseScope converts a string flag to config.Scope.
func parseScope(s string) (config.Scope, error) {
	switch s {
	case "system":
		return config.ScopeSystem, nil
	case "user":
		return config.ScopeUser, nil
	case "project":
		return config.ScopeProject, nil
	default:
		return 0, fmt.Errorf("unknown scope %q: use system, user, or project", s)
	}
}
