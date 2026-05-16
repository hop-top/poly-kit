package router

import (
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print resolved router configuration",
		Long: `Print the resolved routellm configuration as YAML.

Reads config from the default path
($XDG_CONFIG_HOME/hop/llm/router/config.yaml), applies
defaults, and prints the result.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, err := defaultConfigPath()
			if err != nil {
				return fmt.Errorf("resolve config path: %w", err)
			}

			cfg, err := loadRouterYAML(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			out, err := yaml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("marshal config: %w", err)
			}

			fmt.Fprintf(
				cmd.OutOrStdout(),
				"# resolved from %s\n%s", cfgPath, out,
			)
			return nil
		},
	}
	return cmd
}
