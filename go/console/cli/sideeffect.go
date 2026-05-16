package cli

import (
	"github.com/spf13/cobra"
)

// SideEffect classifies what a command does to observable state.
// Agents and the delegation policy engine (see §8.6) read this
// tag to decide whether confirmation, max-ops budget, or policy
// gates apply.
//
// Two generations of values coexist (see ADR-0021):
//
//   - Legacy 4-tier (read|write|destructive|interactive). Still
//     supported. The toolspec walker maps these conservatively into
//     the expanded ladder when projecting Safety.Permissions.
//   - Expanded 6-tier ladder (read|write-local|write-shared|
//     destructive-local|destructive-shared|interactive). Adopters
//     who care about the harness-side scope distinction declare
//     these directly.
//
// Both forms are accepted by Root.Validate. The kit-internal arms
// that gate per side-effect (dry-run install, idempotency-key
// install, policy authorize/confirm) treat the local and shared
// variants as equivalent to their legacy form for write/destructive
// — see isWriteLike / isDestructiveLike helpers in this package.
type SideEffect string

const (
	// SideEffectRead marks a command that performs no state mutation.
	// Safe to retry, safe under any policy (list, show, get, info, find).
	SideEffectRead SideEffect = "read"
	// SideEffectWrite marks a command that mutates state without
	// irreversible loss (create, add, edit, update, sync).
	//
	// Legacy 4-tier value. Toolspec walker maps it to write-shared
	// (the conservative read of the unscoped legacy annotation).
	SideEffectWrite SideEffect = "write"
	// SideEffectWriteLocal marks a command that mutates CWD-scoped
	// state without irreversible loss. The "local" qualifier signals
	// that effects do not propagate beyond the caller's working
	// scope (no shared infra, no upstream).
	SideEffectWriteLocal SideEffect = "write-local"
	// SideEffectWriteShared marks a command that mutates shared
	// infra/upstream state without irreversible loss. Examples: a
	// `git push`, a `tlc sync`, a `publish` to a registry.
	SideEffectWriteShared SideEffect = "write-shared"
	// SideEffectDestructive marks a command that mutates state with
	// potential irreversible loss (delete, reset, rotate, drop).
	// Requires confirmation under default policy.
	//
	// Legacy 4-tier value. Toolspec walker maps it to
	// destructive-shared (conservative).
	SideEffectDestructive SideEffect = "destructive"
	// SideEffectDestructiveLocal marks a command that performs an
	// irreversible local mutation (rm of CWD-scoped state, task
	// delete from a local store). The "local" qualifier signals
	// that the loss is contained.
	SideEffectDestructiveLocal SideEffect = "destructive-local"
	// SideEffectDestructiveShared marks a command that performs an
	// irreversible shared mutation (force-push, drop of a shared
	// database). The harness default policy denies these on a
	// private network egress without explicit allowlist override.
	SideEffectDestructiveShared SideEffect = "destructive-shared"
	// SideEffectInteractive marks a long-running or session-bound
	// command (shell, tui, serve). Agents should treat these as
	// "not-for-batch".
	SideEffectInteractive SideEffect = "interactive"
)

// isWriteLike returns true for any side-effect tier that mutates
// state without irreversible loss. Used by kit's auto-flag arms
// (dry-run install, idempotency-key install) to gate uniformly
// across the legacy 4-tier and expanded 6-tier vocabularies.
func isWriteLike(s SideEffect) bool {
	return s == SideEffectWrite ||
		s == SideEffectWriteLocal ||
		s == SideEffectWriteShared
}

// isDestructiveLike returns true for any side-effect tier that
// performs an irreversible mutation. Used by kit's auto-flag and
// gate arms; mirrors isWriteLike's role for the destructive band.
func isDestructiveLike(s SideEffect) bool {
	return s == SideEffectDestructive ||
		s == SideEffectDestructiveLocal ||
		s == SideEffectDestructiveShared
}

// sideEffectAnnotation is the cobra command annotation key kit reads
// to discover the declared side-effect class. Reserved under the
// kit/ prefix per §3.5 of cli-conventions-with-kit.md.
const sideEffectAnnotation = "kit/side-effect"

// GetSideEffect returns the declared side-effect class on a
// command. Returns ("", false) when the annotation is missing
// (which Root.Validate refuses for leaf commands).
func GetSideEffect(cmd *cobra.Command) (SideEffect, bool) {
	if cmd.Annotations == nil {
		return "", false
	}
	v, ok := cmd.Annotations[sideEffectAnnotation]
	if !ok {
		return "", false
	}
	return SideEffect(v), true
}

// SetSideEffect attaches the kit/side-effect tag in idiomatic form.
// Equivalent to setting cmd.Annotations["kit/side-effect"] = string(s).
func SetSideEffect(cmd *cobra.Command, s SideEffect) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[sideEffectAnnotation] = string(s)
}

// validSideEffects is the closed set Root.Validate accepts.
// Adding a new class here requires updating §3.5 of the
// cli-conventions-with-kit.md spec first.
//
// Both the legacy 4-tier vocabulary and the expanded 6-tier ladder
// (ADR-0021) are valid declarations. The toolspec walker projects
// either into the harness-facing Safety.Permissions vocabulary.
var validSideEffects = map[SideEffect]bool{
	SideEffectRead:              true,
	SideEffectWrite:             true,
	SideEffectWriteLocal:        true,
	SideEffectWriteShared:       true,
	SideEffectDestructive:       true,
	SideEffectDestructiveLocal:  true,
	SideEffectDestructiveShared: true,
	SideEffectInteractive:       true,
}
