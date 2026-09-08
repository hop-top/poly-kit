// Command flagerrtool is the fixture binary for the flag-error exit-code
// tests. It exists as a real binary because exit codes cannot be asserted
// from inside the test process: `go run` masks them, and Root.Execute
// reaches os.Exit on some paths.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	// A read-only leaf's flag that a single edit reaches from a typo, so
	// the autocorrect candidate filter has an unambiguous target.
	list.Flags().Bool("verbose-rows", false, "Verbose rows")

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
			fmt.Fprintln(cmd.OutOrStdout(), "DELETED")
			return nil
		},
	}
	del.Flags().Bool("force", false, "Skip safety checks")

	// A read-only leaf that echoes the flags it actually parsed, so a
	// test can prove the corrected value — not the typed token — is what
	// reached the command. --dry-run support makes the same leaf usable
	// for the "gates see the corrected argv" assertion.
	show := &cobra.Command{
		Use:   "show",
		Short: "Show a thing",
		Long:  "Show a thing.",
		Annotations: map[string]string{
			"kit/side-effect":   "read",
			"kit/idempotent":    "true",
			"kit/dry-run":       "true",
			"kit/output-schema": `{"type":"object"}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			fmt.Fprintf(cmd.OutOrStdout(), "SHOW name=%s dryrun=%v\n", name, cli.IsDryRun(cmd))
			return nil
		},
	}
	show.Flags().String("name", "", "Thing name")

	// Two flags a three-character typo sits equidistant from, so the
	// ambiguity refusal has a case: --stage and --stale are both one
	// edit from --stace.
	amb := &cobra.Command{
		Use:         "ambig",
		Short:       "Ambiguous flag pair",
		Long:        "Ambiguous flag pair.",
		Annotations: map[string]string{"kit/side-effect": "read", "kit/idempotent": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "AMBIG RAN")
			return nil
		},
	}
	amb.Flags().Bool("stage", false, "Stage mode")
	amb.Flags().Bool("stale", false, "Stale mode")

	// A write leaf: correctable in prompt mode only, and never in read
	// mode however close the typo is.
	upd := &cobra.Command{
		Use:   "update",
		Short: "Update a thing",
		Long:  "Update a thing.",
		Annotations: map[string]string{
			"kit/side-effect": "write",
			"kit/idempotent":  "true",
			"kit/dry-run":     "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "UPDATED")
			return nil
		},
	}
	upd.Flags().Bool("force", false, "Skip safety checks")

	// A read-only leaf whose stdin is DATA. If the autocorrect prompt
	// ever reads stdin instead of /dev/tty, the payload this echoes back
	// loses its first line.
	imp := &cobra.Command{
		Use:         "ingest",
		Short:       "Ingest from stdin",
		Long:        "Ingest from stdin.",
		Annotations: map[string]string{"kit/side-effect": "read", "kit/idempotent": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "INGESTED %q\n", string(b))
			return nil
		},
	}
	imp.Flags().Bool("strict", false, "Strict mode")

	r.Cmd.AddCommand(list, del, show, amb, upd, imp)
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
