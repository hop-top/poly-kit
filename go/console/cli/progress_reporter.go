package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/progress"
)

// resolveProgressReporter selects the active progress.Reporter for cmd
// based on flag state. Selection rules (highest precedence first):
//
//  1. --quiet → progress.Discard.
//  2. --progress-format json → progress.JSONL(stderr).
//  3. --progress-format human (or unset) and --format json → JSONL.
//  4. Otherwise → progress.Human(stderr).
//
// All output goes to r.Streams.Human, which is os.Stderr by default.
// Stdout is reserved for the data envelope.
func resolveProgressReporter(cmd *cobra.Command, v *viper.Viper, r *Root) progress.Reporter {
	if v.GetBool("quiet") {
		return progress.Discard()
	}

	w := r.Streams.Human

	pf := cmd.Flags().Lookup("progress-format")
	progressJSON := false
	progressUserSet := pf != nil && pf.Changed
	if pf != nil && v.GetString("progress-format") == "json" {
		progressJSON = true
	}

	// --format json inherits unless --progress-format was set explicitly.
	if !progressJSON && !progressUserSet {
		if v.GetString("format") == "json" {
			progressJSON = true
		}
	}

	if progressJSON {
		return progress.JSONL(w)
	}
	return progress.Human(w)
}
