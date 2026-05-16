package cli

import "github.com/spf13/cobra"

// AutoRegisterFlags walks the command tree and adds any kit-managed
// per-side-effect flags every leaf is required to expose. Today this
// is a no-op for dry-run; future kit-managed per-leaf flags
// (e.g. --confirm on destructive) hook in here.
//
// History (ADR-0019 → ADR-0020): originally this walker installed a
// hidden per-leaf --dry-run cobra flag on every write|destructive
// leaf so cobra would parse `<tool> <leaf> --dry-run` without an
// "unknown flag" error. Under ADR-0020 the kit-global --dry-run
// lives on the root's persistent flag set (registered in cli.New)
// and is inherited by every subcommand automatically; a per-leaf
// flag would shadow that inherited flag and break the viper
// binding (kit.dry_run). The walker therefore no longer registers
// --dry-run; it remains as a registration hook for future
// kit-managed per-leaf flags whose semantics differ from a
// persistent global.
//
// Execute calls AutoRegisterFlags before parse; adopters that bypass
// Execute (calling fang.Execute directly) must invoke this themselves
// before fang.Execute so cobra sees the flags during parsing.
func (r *Root) AutoRegisterFlags() {
	walk(r.Cmd, func(cmd *cobra.Command) {
		if !isLeaf(cmd) || isBuiltin(cmd) {
			return
		}
		if !cmd.Runnable() {
			return
		}
		// Hook point for future kit-managed per-leaf flags.
		// Dry-run is handled by the kit-global persistent flag in cli.New.
	})
}
