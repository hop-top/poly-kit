package cmdmeta

import "github.com/spf13/cobra"

// Dry-run annotation values. Both share KeyDryRun; the value
// distinguishes intent.
const (
	// DryRunSupported is the legacy opt-in marker, retained as a
	// back-compat synonym for tier-driven default-allow.
	DryRunSupported = "supported"
	// DryRunOptedOut is the escape hatch for write/destructive
	// leaves that genuinely cannot honor --dry-run.
	DryRunOptedOut = "opted-out"
)

// IsDryRunSupported reports whether cmd would honor --dry-run under
// the resolved policy. Adopter help renderers and the pre-execution
// check both call this. Returns true for "allow" only; no-op,
// reject-interactive, and reject-opt-out all return false.
//
// Resolution order, matching the policy table cli implements:
//
//  1. kit/dry-run: opted-out → false (explicit author decision).
//  2. kit/dry-run: supported (legacy) → true.
//  3. kit/side-effect = write-like or destructive-like → true.
//  4. kit/side-effect = read → false (silent no-op).
//  5. kit/side-effect = interactive → false (rejected).
//  6. No tag → false (untagged-leaf backstop).
//
// This reads the side-effect tier as a raw string rather than
// through cli's SideEffect type, which is what keeps cmdmeta a leaf
// package. The accepted values are the same closed set cli
// validates against; a value outside it falls through to the
// untagged backstop, exactly as cli's resolver does.
func IsDryRunSupported(cmd *cobra.Command) bool {
	switch read(cmd, KeyDryRun) {
	case DryRunOptedOut:
		return false
	case DryRunSupported:
		return true
	}
	switch read(cmd, KeySideEffect) {
	case "write", "write-local", "write-shared",
		"destructive", "destructive-local", "destructive-shared":
		return true
	}
	return false
}
