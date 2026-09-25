// coverage_command.go mounts `<tool> spec coverage`: the lint mode
// over the reflected tree that reports how much of a CLI's surface
// declares a kit/side-effect class, and fails the process when that
// fraction falls below a threshold.
//
// It hangs off `spec` rather than the root because it answers a
// question about the spec — "how complete is the metadata the
// manifest is built from" — and because `spec` is already reserved,
// so adding a child costs an adopter no top-level verb budget.

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// coverageMinFlag is the threshold flag. Named --min rather than
// --threshold to match the shape of the question ("at least this
// much"), and short enough to read in a CI line.
const coverageMinFlag = "min"

// registerCoverageCommand mounts `spec coverage` under the spec
// subcommand. Called from RegisterSpecCommand so every adopter gets
// the lint mode for free; there is no opt-in, because a coverage
// report an adopter has to remember to mount is a coverage report
// nobody runs.
func registerCoverageCommand(spec *cobra.Command, root *kitcli.Root) {
	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "Report kit/side-effect annotation coverage",
		Long: "Report how many of this tool's commands declare a " +
			"kit/side-effect class, list the ones that do not, and " +
			"exit non-zero when coverage falls below --min.\n\n" +
			"An unannotated command is not a read-only command: it " +
			"is one kit knows nothing about, and every safety gate " +
			"that consumes the manifest must fail closed on it. This " +
			"report is how an adopter finds and closes that gap, and " +
			"how CI keeps it closed.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			"kit/side-effect":     "read",
			"kit/idempotent":      "yes",
			"kit/exit-codes":      "OK,CONFLICT",
			specCommandAnnotation: "true",
		},
	}
	cmd.Flags().Float64(coverageMinFlag, 0,
		"Minimum coverage percentage; exit CONFLICT (4) below it (0 disables the gate)")

	cmd.RunE = func(c *cobra.Command, _ []string) error {
		return runCoverage(c, root)
	}

	_ = kitcli.SetExamples(cmd, []kitcli.Example{
		{
			Title:   "See the gap",
			Command: root.Config.Name + " spec coverage --format json",
		},
		{
			Title:   "Gate in CI",
			Command: root.Config.Name + " spec coverage --min 80",
		},
	})

	spec.AddCommand(cmd)
}

// runCoverage is the `spec coverage` RunE body. It renders the
// report in the active --format and then, separately, applies the
// threshold: the report is printed whether or not the gate passes,
// because a CI failure that does not say which commands to annotate
// is a failure an adopter cannot act on.
func runCoverage(cmd *cobra.Command, root *kitcli.Root) error {
	rep := CoverageOf(root)

	format, changed := flagValueAndChangedWalk(cmd, "format")
	if !changed || format == "" {
		format = output.JSON
	}
	if err := output.Render(cmd.OutOrStdout(), format, rep); err != nil {
		return err
	}

	min, _ := cmd.Flags().GetFloat64(coverageMinFlag)
	if rep.Meets(min) {
		return nil
	}
	if !rep.Measurable {
		// Unmeasurable and gated: say so in those words. Reporting
		// "0% coverage" here would blame the adopter for a tree the
		// reporter could not read.
		return output.ConflictError(fmt.Sprintf(
			"annotation coverage unmeasurable: no reflectable commands in %q "+
				"(--min %.0f%% cannot be evaluated)", rep.Tool, min))
	}
	return output.ConflictError(fmt.Sprintf(
		"annotation coverage %.0f%% (%d/%d) is below --min %.0f%%; unannotated: %s",
		rep.Percent(), rep.Annotated, rep.Total, min,
		strings.Join(rep.UnannotatedPaths, ", ")))
}
