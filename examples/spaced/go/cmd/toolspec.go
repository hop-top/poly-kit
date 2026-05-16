package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// tsSpec mirrors the toolspec YAML schema with yaml tags.
type tsSpec struct {
	Name          string  `yaml:"name"`
	SchemaVersion string  `yaml:"schema_version"`
	Commands      []tsCmd `yaml:"commands"`
}

type tsCmd struct {
	Name     string  `yaml:"name"`
	Children []tsCmd `yaml:"children"`
}

func countCmds(cmds []tsCmd) int {
	n := 0
	for _, c := range cmds {
		n++
		n += countCmds(c.Children)
	}
	return n
}

// ToolspecCmd returns the `toolspec` command.
func ToolspecCmd() *cobra.Command {
	var specPath string

	cmd := &cobra.Command{
		Use:   "toolspec",
		Short: "Load and validate spaced.toolspec.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			if specPath == "" {
				specPath = os.Getenv("SPACED_TOOLSPEC_PATH")
			}
			if specPath == "" {
				// Walk up from cwd looking for the file.
				specPath = findToolspec()
			}

			raw, err := os.ReadFile(specPath)
			if err != nil {
				return fmt.Errorf("read toolspec: %w", err)
			}

			var spec tsSpec
			if err := yaml.Unmarshal(raw, &spec); err != nil {
				return fmt.Errorf("parse toolspec: %w", err)
			}

			var errors []string
			if spec.Name == "" {
				errors = append(errors, "missing required field: name")
			}
			if spec.SchemaVersion == "" {
				errors = append(errors, "missing required field: schema_version")
			}

			fmt.Println()
			fmt.Printf("  Name     : %s\n", spec.Name)
			fmt.Printf("  Version  : %s\n", spec.SchemaVersion)
			fmt.Printf("  Commands : %d\n", countCmds(spec.Commands))

			if len(errors) > 0 {
				fmt.Printf("  Errors   : %d\n", len(errors))
				for _, e := range errors {
					fmt.Printf("    - %s\n", e)
				}
			} else {
				fmt.Println("  Status   : valid")
			}
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().StringVar(&specPath, "spec", "",
		"Path to toolspec YAML (default: auto-detected)")
	return cmd
}

// findToolspec walks up from cwd looking for spaced.toolspec.yaml.
func findToolspec() string {
	const target = "spaced.toolspec.yaml"
	dir, err := os.Getwd()
	if err != nil {
		return target
	}
	for {
		p := filepath.Join(dir, target)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return target
		}
		dir = parent
	}
}
