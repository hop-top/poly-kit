package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// specCommandAnnotation marks the commands toolspec/cli mounts under
// `spec`. Coverage does not measure them, so neither does the
// runnable-group check.
const specCommandAnnotation = "kit/spec-command"

// unclassifiedRunnableGroups lists the runnable command groups — a
// command with children and its own Run — whose kit/side-effect is
// absent, empty, or names no tier. Unannotated paths render bare,
// malformed ones as path="value", the forms `spec coverage` uses.
//
// The set is the one coverage, discovery and the manifest count
// beyond the leaves Validate refuses: the root, framework built-ins,
// kit-reserved verbs and spec commands are skipped, as coverage skips
// them, and a group with no action of its own has nothing to classify.
func (r *Root) unclassifiedRunnableGroups() []string {
	if r == nil || r.Cmd == nil {
		return nil
	}
	var out []string
	walk(r.Cmd, func(cmd *cobra.Command) {
		if cmd == r.Cmd || isLeaf(cmd) || !cmd.Runnable() || isBuiltin(cmd) {
			return
		}
		if r.IsReserved(topAncestorName(cmd, r.Cmd)) ||
			cmd.Annotations[specCommandAnnotation] == "true" {
			return
		}
		s, ok := GetSideEffect(cmd)
		switch {
		case !ok || s == "":
			out = append(out, cmd.CommandPath())
		case !validSideEffects[s]:
			out = append(out, fmt.Sprintf("%s=%q", cmd.CommandPath(), string(s)))
		}
	})
	return out
}

// warnUnclassifiedRunnableGroups writes the runnable groups without a
// tier to stderr, once per root. It warns rather than refuses:
// Validate has only ever checked leaves, and refusing a group now
// would stop tools at boot that ran before. QuietBootWarnings
// silences it.
func (r *Root) warnUnclassifiedRunnableGroups() {
	if r == nil || r.Cmd == nil || r.Config.QuietBootWarnings || r.warnedRunnableGroups {
		return
	}
	paths := r.unclassifiedRunnableGroups()
	if len(paths) == 0 {
		return
	}
	r.warnedRunnableGroups = true
	fmt.Fprintf(r.Cmd.ErrOrStderr(),
		"[warn] runnable command groups without a kit/side-effect tier; "+
			"they run like leaves and are reported unannotated "+
			"(declare one with cli.SetSideEffect; %s):\n  %s\n",
		r.specCoverageHint(), strings.Join(paths, "\n  "))
}
