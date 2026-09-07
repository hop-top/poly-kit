// Command flagerrtool is the fixture binary for the flag-error exit-code
// tests. It exists as a real binary because exit codes cannot be asserted
// from inside the test process: `go run` masks them, and Root.Execute
// reaches os.Exit on some paths.
package main

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

func main() {
	r := cli.New(cli.Config{
		Name:            "flagerrtool",
		Version:         "0.0.0",
		Short:           "Flag error exit-code fixture",
		DisableValidate: true,
	})

	list := &cobra.Command{
		Use:         "list",
		Short:       "List things",
		Long:        "List things.",
		Annotations: map[string]string{"kit/side-effect": "read", "kit/idempotent": "true"},
		RunE:        func(*cobra.Command, []string) error { return nil },
	}
	list.Flags().String("status", "", "Status filter")
	list.Flags().Int("counters", 0, "Counter mode")
	list.Flags().Int("limit", 0, "Max rows")

	// A destructive leaf on the same tree: a mistyped flag here must be
	// refused, never fuzzy-corrected into a run.
	del := &cobra.Command{
		Use:   "delete",
		Short: "Delete things",
		Long:  "Delete things.",
		Annotations: map[string]string{
			"kit/side-effect": "destructive",
			"kit/idempotent":  "false",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Marker on stdout: if this ever prints on a mistyped-flag
			// run, kit auto-applied a guess. That is the failure the
			// exit-code test is guarding.
			cmd.Println("DELETED")
			return nil
		},
	}
	del.Flags().Bool("force", false, "Skip safety checks")

	r.Cmd.AddCommand(list, del)
	r.WithFlagEnum("status", "TODO", "IN_PROGRESS", "DONE", "SKIPPED")

	if err := r.Execute(context.Background()); err != nil {
		os.Exit(exitCodeFor(err))
	}
}

// exitCodeFor mirrors what an adopter main does: read the envelope's exit
// code when the error carries one, else 1.
func exitCodeFor(err error) int {
	var env *output.Error
	if errors.As(err, &env) && env.ExitCode != 0 {
		return env.ExitCode
	}
	return 1
}
