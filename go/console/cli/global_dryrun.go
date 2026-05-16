package cli

import (
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"hop.top/kit/go/runtime/sideeffect"
)

// globalDryRunFlag is the long name of the kit-wide --dry-run flag
// registered on the root persistent flag set in cli.New.
const globalDryRunFlag = "dry-run"

// globalDryRunViperKey is the viper key the global flag binds to.
// Adopters can set this in config (yaml: kit.dry_run: true) or via
// the env var KIT_DRY_RUN; the kit cli auto-resolves both.
const globalDryRunViperKey = "kit.dry_run"

// dryRunAnnotation is the cobra annotation key used by the legacy
// SupportsDryRun opt-in (ADR-0019) and the new OptOutDryRun escape
// hatch (ADR-0020). Both share the key; the value distinguishes
// intent.
const dryRunAnnotation = "kit/dry-run"

// dryRunSupported is the legacy ADR-0019 marker value. Retained as a
// back-compat synonym for "tier-driven default-allow"; logs a
// one-time deprecation warning at startup when found on any leaf.
const dryRunSupported = "supported"

// dryRunOptedOut is the ADR-0020 escape hatch value. Set via
// OptOutDryRun for write|destructive leaves that genuinely cannot
// honor --dry-run (compound state half-applied, downstream services
// without preview semantics, etc.). The pre-execution hook rejects
// --dry-run on these with a friendlier diagnostic than the generic
// "not supported."
const dryRunOptedOut = "opted-out"

// dryRunPolicy is the resolved decision the pre-execution hook makes
// per leaf. See ADR-0020 for the policy table.
type dryRunPolicy int

const (
	// dryRunPolicyAllow: --dry-run is honored; ctx is tagged via
	// sideeffect.WithDryRun and RunE observes IsDryRun(ctx)=true.
	dryRunPolicyAllow dryRunPolicy = iota
	// dryRunPolicyNoOp: --dry-run is accepted silently (no ctx tag,
	// no error). Used for read-tier leaves where the flag is
	// meaningless but rejecting is noisy (shell-history mistakes).
	dryRunPolicyNoOp
	// dryRunPolicyRejectInteractive: --dry-run is refused with a
	// friendly diagnostic explaining interactive sessions have no
	// batch-boundary to scope the preview.
	dryRunPolicyRejectInteractive
	// dryRunPolicyRejectOptOut: --dry-run is refused because the
	// command author called OptOutDryRun(cmd). Diagnostic points at
	// the explicit decision rather than implying the command is
	// unmigrated.
	dryRunPolicyRejectOptOut
	// dryRunPolicyRejectUntagged: --dry-run is refused because the
	// command has no kit/side-effect tag and no legacy
	// SupportsDryRun annotation. Root.Validate normally catches
	// this earlier; we keep the policy as a backstop so adopters
	// running with EnforceValidate=false still get a coherent
	// answer.
	dryRunPolicyRejectUntagged
)

// SupportsDryRun marks cmd as honoring --dry-run.
//
// Deprecated under ADR-0020: prefer the kit/side-effect tier alone.
// This function is retained as a back-compat synonym and remains
// safe to call: it sets kit/dry-run: supported, which the resolver
// treats as "allow" (with a one-time deprecation log at startup).
//
// Use OptOutDryRun for the rare write|destructive command that
// genuinely cannot honor dry-run.
func SupportsDryRun(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[dryRunAnnotation] = dryRunSupported
}

// OptOutDryRun marks cmd as NOT honoring --dry-run, even though its
// kit/side-effect tier (write|destructive) would otherwise opt it in
// by default. Use for compound-state commands whose dryrun impl
// cannot keep state honest, or for write commands that shell out to
// a third-party API without preview semantics.
//
// The pre-execution hook rejects --dry-run on opted-out commands
// with a friendly diagnostic. Adopter doc-comments should explain
// why the opt-out is necessary so the audit trail survives.
func OptOutDryRun(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[dryRunAnnotation] = dryRunOptedOut
}

// resolveDryRunPolicy returns the policy decision for cmd per
// ADR-0020. Resolution order:
//
//  1. kit/dry-run: opted-out → reject (explicit author decision).
//  2. kit/dry-run: supported (legacy) → allow.
//  3. kit/side-effect = write|destructive → allow.
//  4. kit/side-effect = read → silent no-op.
//  5. kit/side-effect = interactive → reject with diagnostic.
//  6. No tag → reject (untagged-leaf backstop).
func resolveDryRunPolicy(cmd *cobra.Command) dryRunPolicy {
	if cmd == nil {
		return dryRunPolicyRejectUntagged
	}
	if cmd.Annotations != nil {
		switch cmd.Annotations[dryRunAnnotation] {
		case dryRunOptedOut:
			return dryRunPolicyRejectOptOut
		case dryRunSupported:
			return dryRunPolicyAllow
		}
	}
	s, ok := GetSideEffect(cmd)
	if !ok {
		return dryRunPolicyRejectUntagged
	}
	if isWriteLike(s) || isDestructiveLike(s) {
		return dryRunPolicyAllow
	}
	switch s {
	case SideEffectRead:
		return dryRunPolicyNoOp
	case SideEffectInteractive:
		return dryRunPolicyRejectInteractive
	}
	return dryRunPolicyRejectUntagged
}

// IsDryRunSupported reports whether cmd would honor --dry-run under
// the resolved policy. Adopter help renderers and the pre-execution
// check both call this. Returns true for "allow" only; no-op,
// reject-interactive, and reject-opt-out all return false.
func IsDryRunSupported(cmd *cobra.Command) bool {
	return resolveDryRunPolicy(cmd) == dryRunPolicyAllow
}

// dryRunHelpAddendum is the line appended to a leaf command's Long
// description so `<tool> help <cmd>` advertises dry-run support
// state. We only append; we never overwrite an adopter's Long.
const dryRunHelpAddendum = "\n\nDry-run support: this command honors --dry-run."

// applyDryRunHelpAddendum walks the command tree and appends the
// dry-run support marker to every leaf whose resolved policy is
// "allow." Idempotent: re-running on the same tree does not
// duplicate the marker. Called from Root.Execute alongside
// AutoRegisterFlags.
func (r *Root) applyDryRunHelpAddendum() {
	if r == nil || r.Cmd == nil {
		return
	}
	walk(r.Cmd, func(cmd *cobra.Command) {
		if !isLeaf(cmd) || isBuiltin(cmd) {
			return
		}
		if resolveDryRunPolicy(cmd) != dryRunPolicyAllow {
			return
		}
		if strings.Contains(cmd.Long, dryRunHelpAddendum) {
			return
		}
		cmd.Long += dryRunHelpAddendum
	})
}

// legacyDryRunWarnOnce ensures the deprecation log line for legacy
// kit/dry-run: supported usage fires at most once per process. The
// warning is best-effort: tests that fail to assert against it must
// not fail the process if the warning has already been logged.
var legacyDryRunWarnOnce sync.Once

// warnLegacySupportsDryRun emits a one-time deprecation note when
// any leaf in the command tree carries the legacy
// kit/dry-run: supported annotation. ADR-0020 keeps the annotation
// as a back-compat synonym; the warning makes the deprecation
// audible without breaking adopters mid-migration.
func (r *Root) warnLegacySupportsDryRun() {
	if r == nil || r.Cmd == nil {
		return
	}
	var found bool
	walk(r.Cmd, func(cmd *cobra.Command) {
		if found {
			return
		}
		if cmd.Annotations != nil && cmd.Annotations[dryRunAnnotation] == dryRunSupported {
			found = true
		}
	})
	if !found {
		return
	}
	legacyDryRunWarnOnce.Do(func() {
		fmt.Fprintln(r.Cmd.ErrOrStderr(),
			"[deprecation] kit/dry-run: supported annotation is "+
				"superseded by ADR-0020. Drop the explicit "+
				"cli.SupportsDryRun(cmd) call; the kit/side-effect "+
				"tier already opts write|destructive leaves into "+
				"--dry-run by default.")
	})
}

// installDryRunHook returns a PersistentPreRunE func that wraps the
// command's context with sideeffect.WithDryRun when the global flag
// is set, and applies the ADR-0020 policy table. Composes into the
// kit PersistentPreRunE chain via Hooks.PrePersistentRunE.
func (r *Root) installDryRunHook() func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		if r == nil || r.Viper == nil {
			return nil
		}
		// Read viper rather than cobra: the global flag is bound to
		// viper, and viper is the source of truth that also picks up
		// env/config defaults if the adopter wired them.
		on := r.Viper.GetBool(globalDryRunViperKey)
		if !on {
			return nil
		}
		// Help/completion are exempt — they don't dispatch RunE.
		if !isLeaf(cmd) || isBuiltin(cmd) {
			return nil
		}
		switch resolveDryRunPolicy(cmd) {
		case dryRunPolicyAllow:
			cmd.SetContext(sideeffect.WithDryRun(cmd.Context(), true))
			return nil
		case dryRunPolicyNoOp:
			// Read-tier: accept the flag silently. Don't tag ctx —
			// reads have no side effects to preview.
			return nil
		case dryRunPolicyRejectInteractive:
			return fmt.Errorf(
				"--dry-run is not meaningful for interactive commands "+
					"(%q); interactive sessions have no batch boundary "+
					"to scope the preview; run without --dry-run",
				cmd.CommandPath())
		case dryRunPolicyRejectOptOut:
			return fmt.Errorf(
				"--dry-run is not supported by %q: the command "+
					"explicitly opted out via cli.OptOutDryRun; "+
					"run without --dry-run; see the command's "+
					"documentation for the rationale",
				cmd.CommandPath())
		case dryRunPolicyRejectUntagged:
			return fmt.Errorf(
				"--dry-run cannot be applied to %q: the command "+
					"is missing the required kit/side-effect tag; "+
					"adopter must call cli.SetSideEffect(cmd, ...) "+
					"to declare the tier",
				cmd.CommandPath())
		}
		return nil
	}
}
