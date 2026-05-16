package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// SignatureViolation reports a single failure surfaced by
// Root.ValidateSignature. Path is the cobra CommandPath of the
// offending node, Check is one of the canonical check IDs
// ("local-globals" | "reserved-name" | "depth-hierarchical" |
// "passthrough"), Detail is a human-readable explanation, and
// Severity is "error" (would trip reject mode) or "warning" (logged
// only).
type SignatureViolation struct {
	Path     string `json:"path"`
	Check    string `json:"check"`
	Detail   string `json:"detail"`
	Severity string `json:"severity"`
}

// SignatureReport collects the violations emitted by a
// ValidateSignature walk. The zero value is an empty report;
// HasViolations reports whether any entries are present.
type SignatureReport struct {
	Violations []SignatureViolation `json:"violations"`
}

// HasViolations reports whether the report carries at least one
// entry. nil-safe.
func (r *SignatureReport) HasViolations() bool {
	if r == nil {
		return false
	}
	return len(r.Violations) > 0
}

// add appends a violation. Internal helper; tests reach for
// Violations directly.
func (r *SignatureReport) add(v SignatureViolation) {
	if r == nil {
		return
	}
	r.Violations = append(r.Violations, v)
}

// Canonical check identifiers. Keep stable for JSON consumers.
const (
	SignatureCheckLocalGlobals      = "local-globals"
	SignatureCheckReservedName      = "reserved-name"
	SignatureCheckDepthHierarchical = "depth-hierarchical"
	SignatureCheckPassthrough       = "passthrough"
)

// RenderText writes a human-readable rendering of the report to w.
// Empty reports emit a single "no signature violations" line so
// pipelines can rely on a non-empty stdout.
func (r *SignatureReport) RenderText(w io.Writer) error {
	if r == nil || len(r.Violations) == 0 {
		_, err := fmt.Fprintln(w, "no signature violations")
		return err
	}
	for _, v := range r.Violations {
		if _, err := fmt.Fprintf(w, "%s [%s/%s] %s\n",
			v.Path, v.Check, v.Severity, v.Detail); err != nil {
			return err
		}
	}
	return nil
}

// RenderJSON writes the report as a single JSON object to w. Empty
// reports still emit a stable shape `{"violations":[]}`.
func (r *SignatureReport) RenderJSON(w io.Writer) error {
	out := r
	if out == nil {
		out = &SignatureReport{}
	}
	// Ensure non-nil slice for stable serialization.
	if out.Violations == nil {
		out = &SignatureReport{Violations: []SignatureViolation{}}
	}
	enc := json.NewEncoder(w)
	return enc.Encode(out)
}

// ValidateSignature walks r.Cmd and runs the four signature checks:
// local-globals, reserved-name, depth-hierarchical, passthrough.
// Returns a non-nil *SignatureReport (possibly empty). The walk runs
// regardless of Config.SignatureStrictness; the strictness mode
// gates how Execute() reacts to the result.
//
// Built-in commands (completion, the auto-help, anything marked
// kit/exempt-validation) are skipped — same exemption set as the
// Layer-A validator.
func (r *Root) ValidateSignature() *SignatureReport {
	report := &SignatureReport{}
	if r == nil || r.Cmd == nil {
		return report
	}
	r.checkSignatureLocalGlobals(report)
	r.checkSignatureReservedName(report)
	r.checkSignatureDepthHierarchical(report)
	r.checkSignaturePassthrough(report)
	return report
}

// signatureGlobalFlagSet returns the set of persistent-flag names
// owned by r.Cmd. Leaves that re-register any of these names shadow
// the global. Includes both kit-shipped globals (--format, --quiet,
// --no-color, --chdir, --dry-run, --progress-format, -c/--config,
// --no-hints, --confirm, --max-ops, --policy, --api-version,
// --verbose) and any adopter-declared Config.Globals.
func (r *Root) signatureGlobalFlagSet() map[string]struct{} {
	out := map[string]struct{}{}
	if r == nil || r.Cmd == nil {
		return out
	}
	r.Cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		out[f.Name] = struct{}{}
	})
	return out
}

// checkSignatureLocalGlobals walks every leaf and reports leaves
// that own a flag whose name is in the root's persistent-flag set.
// Algorithm:
//
//  1. Snapshot the root's PersistentFlags() names into a set.
//  2. For each leaf, iterate Flags() (LOCAL flags only — not the
//     inherited persistent ones).
//  3. If a local flag's name is in the global set, record a
//     violation.
//
// Implementation note. cobra distinguishes local from inherited via
// distinct flagsets internally; cmd.Flags() merges them for
// iteration via VisitAll, while cmd.LocalFlags() returns the local
// set only. We use LocalFlags() to avoid false positives — every
// leaf "sees" the inherited persistent flags through Flags(), but
// only flags it registered itself trip the shadow.
func (r *Root) checkSignatureLocalGlobals(report *SignatureReport) {
	if r == nil || r.Cmd == nil {
		return
	}
	globals := r.signatureGlobalFlagSet()
	if len(globals) == 0 {
		return
	}
	walk(r.Cmd, func(cmd *cobra.Command) {
		if cmd == r.Cmd || isBuiltin(cmd) {
			return
		}
		if !isLeaf(cmd) || !cmd.Runnable() {
			return
		}
		// LocalFlags() returns only flags registered on cmd itself
		// (not inherited persistent flags from ancestors). This is
		// the flag set that, if it names a global, shadows it.
		var shadowed []string
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if _, isGlobal := globals[f.Name]; isGlobal {
				shadowed = append(shadowed, f.Name)
			}
		})
		if len(shadowed) == 0 {
			return
		}
		sort.Strings(shadowed)
		report.add(SignatureViolation{
			Path:     cmd.CommandPath(),
			Check:    SignatureCheckLocalGlobals,
			Detail:   "leaf redefines global flag(s): " + strings.Join(shadowed, ", "),
			Severity: "error",
		})
	})
}

// checkSignatureReservedName walks every parent that declares
// kit/reserves-children and reports any direct child whose Name()
// is in the parent's reservation set.
func (r *Root) checkSignatureReservedName(report *SignatureReport) {
	if r == nil || r.Cmd == nil {
		return
	}
	walk(r.Cmd, func(cmd *cobra.Command) {
		if isBuiltin(cmd) {
			return
		}
		reserved := GetReservesChildren(cmd)
		if len(reserved) == 0 {
			return
		}
		blockSet := make(map[string]struct{}, len(reserved))
		for _, n := range reserved {
			blockSet[n] = struct{}{}
		}
		for _, c := range cmd.Commands() {
			if isBuiltin(c) {
				continue
			}
			if _, blocked := blockSet[c.Name()]; blocked {
				report.add(SignatureViolation{
					Path:     c.CommandPath(),
					Check:    SignatureCheckReservedName,
					Detail:   fmt.Sprintf("child name %q is reserved by parent %q", c.Name(), cmd.CommandPath()),
					Severity: "error",
				})
			}
		}
	})
}

// checkSignatureDepthHierarchical walks every leaf at depth >= 3 and
// requires that every intermediate (non-root, non-leaf) ancestor
// carry kit/hierarchical=true. Stricter than the Layer-A
// depthThreeAncestorOK, which lets reserved depth-1 ancestors off
// the hook — the signature validator requires the explicit
// annotation regardless of ancestry reservation status.
func (r *Root) checkSignatureDepthHierarchical(report *SignatureReport) {
	if r == nil || r.Cmd == nil {
		return
	}
	walkDepth(r.Cmd, 0, func(cmd *cobra.Command, depth int) {
		if cmd == r.Cmd || isBuiltin(cmd) {
			return
		}
		if !isLeaf(cmd) || !cmd.Runnable() {
			return
		}
		if depth < 3 {
			return
		}
		// Walk ancestors from cmd's parent up to (but excluding) the
		// root; require kit/hierarchical on each intermediate.
		var missing []string
		for c := cmd.Parent(); c != nil && c != r.Cmd; c = c.Parent() {
			if !IsHierarchical(c) {
				missing = append(missing, c.CommandPath())
			}
		}
		if len(missing) == 0 {
			return
		}
		// missing is leaf->root order; flip for readability.
		for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
			missing[i], missing[j] = missing[j], missing[i]
		}
		report.add(SignatureViolation{
			Path:  cmd.CommandPath(),
			Check: SignatureCheckDepthHierarchical,
			Detail: fmt.Sprintf(
				"depth %d leaf requires kit/hierarchical on intermediate(s): %s",
				depth, strings.Join(missing, ", ")),
			Severity: "error",
		})
	})
}

// SignatureReportError wraps a SignatureReport so reject mode can
// flow through ValidationFailureMode (which expects an error).
// Error() emits a one-line summary; callers that want full per-
// violation detail render the report directly.
type SignatureReportError struct {
	Report *SignatureReport
}

// Error implements the error interface.
func (e *SignatureReportError) Error() string {
	if e == nil || e.Report == nil || len(e.Report.Violations) == 0 {
		return "signature validation failed"
	}
	parts := make([]string, 0, len(e.Report.Violations))
	for _, v := range e.Report.Violations {
		parts = append(parts, fmt.Sprintf("%s [%s] %s", v.Path, v.Check, v.Detail))
	}
	return "signature validation failed: " + strings.Join(parts, "; ")
}

// dispatchSignatureReport runs ValidateSignature and reacts per
// Config.SignatureStrictness. Returns (handled, returned) where
// handled=true means Execute should return immediately with
// `returned` as its error. silent never reaches this path
// (Execute filters it out). warn logs and returns (false, nil).
// reject routes the SignatureReportError through
// ValidationFailureMode.
func (r *Root) dispatchSignatureReport() (bool, error) {
	report := r.ValidateSignature()
	if !report.HasViolations() {
		return false, nil
	}
	switch r.Config.SignatureStrictness {
	case SignatureStrictnessWarn:
		for _, v := range report.Violations {
			slog.Warn("cli signature violation",
				"path", v.Path,
				"check", v.Check,
				"detail", v.Detail,
				"severity", v.Severity)
		}
		return false, nil
	case SignatureStrictnessReject:
		return r.dispatchValidationFailure(&SignatureReportError{Report: report})
	default:
		// silent — Execute() filters this out before calling here.
		return false, nil
	}
}

// checkSignaturePassthrough walks every leaf using
// cobra.ArbitraryArgs and reports any that lack the kit/passthrough
// annotation. ArbitraryArgs accepts anything cobra passes through,
// so without the annotation the surface is wider than the contract
// allows. The check uses reflect to compare function pointers
// because cobra.PositionalArgs is a func type with no Equal method.
func (r *Root) checkSignaturePassthrough(report *SignatureReport) {
	if r == nil || r.Cmd == nil {
		return
	}
	arbitraryPtr := reflect.ValueOf(cobra.ArbitraryArgs).Pointer()
	walk(r.Cmd, func(cmd *cobra.Command) {
		if cmd == r.Cmd || isBuiltin(cmd) {
			return
		}
		if !isLeaf(cmd) || !cmd.Runnable() {
			return
		}
		if cmd.Args == nil {
			return
		}
		if reflect.ValueOf(cmd.Args).Pointer() != arbitraryPtr {
			return
		}
		if IsPassthrough(cmd) {
			return
		}
		report.add(SignatureViolation{
			Path:     cmd.CommandPath(),
			Check:    SignatureCheckPassthrough,
			Detail:   "leaf uses cobra.ArbitraryArgs without kit/passthrough annotation",
			Severity: "warning",
		})
	})
}
