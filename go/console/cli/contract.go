package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// Annotation keys reserved under the kit/ prefix for the contract
// surface . See design 01
// for the rationale.
//
// All values are string-encoded so the kit/* namespace stays uniform
// across Go, TypeScript, and Python SDKs (cobra Annotations is the
// wire format).
const (
	// kitRetryable is "true" when the command's effect is safely
	// re-runnable without further protection (after a transient
	// failure, etc.). Absence means "false".
	kitRetryable = "kit/retryable"
	// kitDryRunRationale carries the adopter-supplied 1-200 char
	// reason a write|destructive leaf opted out of --dry-run. Read
	// by the EnforceDryRunRationale validator gate.
	kitDryRunRationale = "kit/dry-run-rationale"
	// kitOutputSchema carries the adopter-declared JSON Schema for
	// the command's structured output. Pre-serialized JSON; the
	// validator parses it shallowly to ensure it is valid JSON.
	kitOutputSchema = "kit/output-schema"
	// kitOutputSchemaVersion carries the adopter-declared
	// MAJOR.MINOR version paired with kit/output-schema.
	kitOutputSchemaVersion = "kit/output-schema-version"
	// kitExamples carries a JSON-encoded []Example (Title, Command,
	// Output). Read by `<tool> spec --format json` and by adopter
	// help renderers.
	kitExamples = "kit/examples"
	// kitNextSteps carries a JSON-encoded []NextStep (When, Suggest,
	// Reason). Surfaced to agents post-invocation.
	kitNextSteps = "kit/next-steps"
	// kitTopLevelVerb marks a depth-1 leaf as an intentional
	// top-level verb (e.g. `kit init`). Without it the shape
	// validator rejects depth-1 runnable leaves.
	kitTopLevelVerb = "kit/top-level-verb"
	// kitHierarchical marks an intermediate non-runnable node as an
	// intentional grouping level for depth-3+ trees. Required on
	// every intermediate when the leaf depth exceeds 2.
	kitHierarchical = "kit/hierarchical"
	// kitPassthrough marks a leaf that accepts opaque positional
	// `-- args...` and forwards them to a child process. Purely
	// informational; surfaces in the spec manifest.
	kitPassthrough = "kit/passthrough"
	// kitExemptValidation marks an internal command exempt from
	// Layer-A enforcement. Reserved for kit-shipped commands that
	// can't reasonably carry the full annotation set (compat
	// shims, debug-only stubs). Adopter use is discouraged —
	// prefer annotating.
	kitExemptValidation = "kit/exempt-validation"

	// kitFormatFlag carries the flag-name (with value) the
	// harness appends to argv to elicit JSON output from a leaf.
	// Default when absent: "--format=json". Adopters with a
	// different invocation style (e.g. "--output=json") declare it
	// explicitly via SetFormatFlag.
	kitFormatFlag = "kit/format-flag"
	// kitReservesChildren is a comma-separated list of child names
	// that an intermediate node refuses to accept on its subcommand
	// list. The signature validator's reserved-name check reads this
	// to catch adopters who mount a child whose name collides with
	// the parent's reservation set. Empty / unset = no extra
	// reservations beyond the kit-shipped defaults.
	kitReservesChildren = "kit/reserves-children"
)

// defaultMaxGuidanceBytes caps the byte size of kit/examples +
// kit/next-steps annotation values. Adopters can raise this via
// Config.MaxGuidanceBytes. 16 KiB is roomy enough for 10-20 examples
// and still bounds the manifest.
const defaultMaxGuidanceBytes = 16384

// SetRetryable attaches the kit/retryable annotation. Pass true for
// commands whose effect is safely re-runnable (after transient
// failure, in particular).
func SetRetryable(cmd *cobra.Command, v bool) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[kitRetryable] = strconv.FormatBool(v)
}

// IsRetryable reports whether kit/retryable is "true" on cmd.
func IsRetryable(cmd *cobra.Command) bool {
	if cmd == nil || cmd.Annotations == nil {
		return false
	}
	return cmd.Annotations[kitRetryable] == "true"
}

// SetDestructiveToken marks cmd as requiring --confirm-token=<sha> in
// addition to --confirm. Equivalent to setting
// kit/destructive-token=required directly.
func SetDestructiveToken(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[destructiveTokenAnnotation] = "required"
}

// SetDryRunRationale attaches the adopter-supplied reason for opting
// out of --dry-run on a write|destructive leaf. The string must be
// 1-200 chars; longer values are rejected.
func SetDryRunRationale(cmd *cobra.Command, reason string) error {
	if cmd == nil {
		return fmt.Errorf("SetDryRunRationale: nil command")
	}
	if l := len(reason); l == 0 || l > 200 {
		return fmt.Errorf("SetDryRunRationale: reason must be 1-200 chars (got %d)", l)
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[kitDryRunRationale] = reason
	return nil
}

// IsReadOnly reports whether cmd's declared kit/side-effect is "read".
func IsReadOnly(cmd *cobra.Command) bool {
	s, ok := GetSideEffect(cmd)
	if !ok {
		return false
	}
	return s == SideEffectRead
}

// IsMutating reports whether cmd's declared kit/side-effect is in the
// write|destructive bands (any tier).
func IsMutating(cmd *cobra.Command) bool {
	s, ok := GetSideEffect(cmd)
	if !ok {
		return false
	}
	return isWriteLike(s) || isDestructiveLike(s)
}

// SetExemptValidation marks cmd as exempt from Layer-A enforcement.
// Reserved for kit-shipped commands that cannot reasonably carry the
// full annotation set (compat shims, hidden debug stubs); adopter
// commands should annotate properly instead.
func SetExemptValidation(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[kitExemptValidation] = "true"
}

// IsDestructive reports whether cmd's declared kit/side-effect is in
// the destructive band (legacy or expanded tier).
func IsDestructive(cmd *cobra.Command) bool {
	s, ok := GetSideEffect(cmd)
	if !ok {
		return false
	}
	return isDestructiveLike(s)
}

// SetFormatFlag attaches the kit/format-flag annotation. Pass the
// full flag with its value, e.g. "--format=json" or
// "--output=json". The conformance harness reads this to discover
// how to elicit a JSON-shaped stdout from a leaf when validating
// against kit/output-schema. Empty value clears the annotation.
func SetFormatFlag(cmd *cobra.Command, flagAndValue string) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	if flagAndValue == "" {
		delete(cmd.Annotations, kitFormatFlag)
		return
	}
	cmd.Annotations[kitFormatFlag] = flagAndValue
}

// GetFormatFlag returns the kit/format-flag value on cmd, or
// "--format=json" when absent. Adopters who want the strict
// "annotation present?" check should read cmd.Annotations directly.
func GetFormatFlag(cmd *cobra.Command) string {
	if cmd == nil || cmd.Annotations == nil {
		return "--format=json"
	}
	if v := cmd.Annotations[kitFormatFlag]; v != "" {
		return v
	}
	return "--format=json"
}

// SetReservesChildren attaches the kit/reserves-children annotation
// to cmd. The names argument is the set of child-command names cmd
// refuses to mount; any subcommand whose Name() appears here trips
// the signature validator's reserved-name check. Pass an empty slice
// or nil to clear the annotation.
//
// Names are stored as a comma-separated string for cross-language
// parity (cobra Annotations is map[string]string). Duplicate names
// are collapsed; ordering is preserved.
func SetReservesChildren(cmd *cobra.Command, names []string) {
	if cmd == nil {
		return
	}
	if len(names) == 0 {
		if cmd.Annotations != nil {
			delete(cmd.Annotations, kitReservesChildren)
		}
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	seen := make(map[string]struct{}, len(names))
	uniq := make([]string, 0, len(names))
	for _, n := range names {
		t := strings.TrimSpace(n)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		uniq = append(uniq, t)
	}
	cmd.Annotations[kitReservesChildren] = strings.Join(uniq, ",")
}

// GetReservesChildren returns the kit/reserves-children list on cmd,
// or nil when the annotation is absent or empty. The result is a
// fresh slice; callers may mutate it without affecting the
// underlying annotation.
func GetReservesChildren(cmd *cobra.Command) []string {
	if cmd == nil || cmd.Annotations == nil {
		return nil
	}
	raw := cmd.Annotations[kitReservesChildren]
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
