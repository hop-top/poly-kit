package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"hop.top/kit/go/core/compliance"
)

// ComplianceCmd returns the `compliance` command.
func ComplianceCmd() *cobra.Command {
	var (
		staticOnly bool
		format     string
		specPath   string
	)

	cmd := &cobra.Command{
		Use:   "compliance",
		Short: "Run 12-factor AI CLI compliance checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			if specPath == "" {
				specPath = os.Getenv("SPACED_TOOLSPEC_PATH")
			}
			if specPath == "" {
				specPath = findToolspec()
			}

			var binaryPath string
			if !staticOnly {
				bin, err := os.Executable()
				if err == nil {
					binaryPath = bin
				}
			}

			report, err := compliance.Run(binaryPath, specPath)
			if err != nil {
				return fmt.Errorf("compliance check: %w", err)
			}

			fmt.Print(compliance.FormatReport(report, format))
			return nil
		},
	}

	cmd.Flags().BoolVar(&staticOnly, "static", false,
		"Run static checks only (no binary execution)")
	cmd.Flags().StringVar(&format, "format", "text",
		"Output format (text, json)")
	cmd.Flags().StringVar(&specPath, "spec", "",
		"Path to toolspec YAML (default: auto-detected)")
	return cmd
}
