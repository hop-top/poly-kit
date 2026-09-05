package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"hop.top/kit/go/transport/cmdsurface"
)

// ResetFlags resets all flag values on cmd and its children to their
// defaults, clearing each flag's Changed bit with its value.
//
// [Root.Execute] does this itself before every dispatch, so a Root
// executed repeatedly needs no explicit call. It stays exported for
// adopters driving a tree through cobra directly.
//
// The per-flag work is [cmdsurface.ResetFlagToDefault], the same
// primitive the in-process runner uses between served invocations, so
// both paths agree on pflag's edge cases: a slice flag is emptied
// rather than appended to, and a callback-backed flag is left alone
// so a reset never invokes the adopter's own function. Viper bindings
// are preserved — they re-read from the flag on next access.
func ResetFlags(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	seen := map[*pflag.Flag]bool{}
	resetFlagSet(cmd, seen)
}

// resetFlagSet resets cmd's own and persistent flags, then recurses.
// Persistent flags are shared by pointer into every descendant's
// merged set, so seen keeps each flag to a single reset.
func resetFlagSet(cmd *cobra.Command, seen map[*pflag.Flag]bool) {
	reset := func(f *pflag.Flag) {
		if seen[f] {
			return
		}
		seen[f] = true
		cmdsurface.ResetFlagToDefault(f)
	}
	cmd.Flags().VisitAll(reset)
	cmd.PersistentFlags().VisitAll(reset)
	for _, child := range cmd.Commands() {
		resetFlagSet(child, seen)
	}
}

// resetForExecute returns the tree to its defaults before a repeat
// [Root.Execute] parses a new command line onto it.
//
// The first Execute is left alone: whatever the flags hold then is
// what construction and the adopter's own setup put there, and is
// part of this run's intended state. Only a second and later call has
// a previous parse to undo.
func (r *Root) resetForExecute() {
	if r == nil || r.Cmd == nil {
		return
	}
	if !r.executed {
		r.executed = true
		return
	}
	ResetFlags(r.Cmd)
}

// Reset restores all flags to defaults and clears args.
// After Reset, subsequent Execute() calls run with an explicit
// empty arg list rather than falling back to os.Args.
func (r *Root) Reset() {
	ResetFlags(r.Cmd)
	r.SetArgs([]string{})
}
