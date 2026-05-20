// status.go implements `kit telemetry status`: a read-only inspector
// over the persisted consent decision, the anonymous installation
// identifier, and the active telemetry Mode. Pure I/O (no mutation);
// safe to run from CI, scripts, and pre-commit hooks.
//
// Output is dispatched by the kit-wide `--format` flag (table / json /
// yaml / text — table is the human default). JSON and YAML are stable
// for scripted audits; the table render groups the data into three
// section headings (Consent / Identity / Mode) to mirror the operator's
// mental model of "the three things to know about telemetry".

package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/consent"
	runtimetel "hop.top/kit/go/runtime/telemetry"
)

// StatusOutput is the structured payload emitted by `kit telemetry
// status --format json|yaml`. Field tags pin the wire vocabulary so
// downstream scripts can lock against the schema without depending on
// Go-side names. Subfields are flat-grouped (Consent / Identity / Mode)
// because operators read this output to answer "am I shipping?", "what
// id am I shipping under?", and "what tier am I shipping at?" — three
// distinct questions, three distinct sections.
type StatusOutput struct {
	Consent  ConsentInfo  `json:"consent"  yaml:"consent"`
	Identity IdentityInfo `json:"identity" yaml:"identity"`
	Mode     ModeInfo     `json:"mode"     yaml:"mode"`
}

// ConsentInfo mirrors consent.Decision as a wire-stable, string-only
// payload. DecidedAt is serialized as RFC3339 (empty string when the
// decision is StateUnknown) so JSON consumers don't have to handle
// Go's zero-time sentinel.
type ConsentInfo struct {
	State          string `json:"state"           yaml:"state"`
	DecidedAt      string `json:"decided_at"      yaml:"decided_at"`
	PromptVersion  int    `json:"prompt_version"  yaml:"prompt_version"`
	DecisionSource string `json:"decision_source" yaml:"decision_source"`
}

// IdentityInfo carries the anonymous installation identifier and its
// on-disk location. When the id read errors (corrupt file, permission
// issue), the error is folded into InstallationID as "(error: <msg>)"
// so the rest of the report still renders.
type IdentityInfo struct {
	InstallationID string `json:"installation_id" yaml:"installation_id"`
	Path           string `json:"path"            yaml:"path"`
}

// ModeInfo reports the active telemetry tier (off / anon / full) and
// the registered application prefix (empty when kit is the embedding
// binary — most kit users see "(none)" here in the table render).
type ModeInfo struct {
	Current   string `json:"current"    yaml:"current"`
	AppPrefix string `json:"app_prefix" yaml:"app_prefix"`
}

// statusCmd builds the `kit telemetry status` leaf. RunE delegates to
// runStatus so the command body stays trivially exercisable from
// tests without spinning a cobra invocation.
func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current telemetry consent + mode",
		Long: `Show the resolved telemetry consent state, the anonymous
installation identifier, and the active emission Mode.

This command performs no network calls and writes no state. Render
format is controlled by the kit-wide --format flag (table | json |
yaml | text). The JSON and YAML payloads are stable for scripted
audits; the table format groups the data into Consent / Identity /
Mode sections for terminal reading.`,
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			"kit/side-effect": "read",
			"kit/idempotent":  "yes",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(
				cmd.Context(),
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
				resolveFormat(cmd),
			)
		},
	}
}

// resolveFormat walks the parent chain to find the active --format
// value. Matches the resolveAuthStatusFormat pattern in
// hop.top/kit/go/console/cli/auth_status.go so kit subcommands surface
// the same fallback semantics: explicit --format wins; if unset or
// empty, default to "table".
func resolveFormat(cmd *cobra.Command) string {
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.Flags().Lookup("format"); f != nil {
			if v := f.Value.String(); v != "" {
				return v
			}
		}
		if pf := c.PersistentFlags().Lookup("format"); pf != nil {
			if v := pf.Value.String(); v != "" {
				return v
			}
		}
	}
	return output.Table
}

// runStatus is the testable core of `kit telemetry status`. It
// gathers data from the consent store, the installation_id helper,
// and the runtime mode globals, then dispatches to the requested
// renderer. stderr is reserved for non-fatal diagnostics (none today;
// kept in the signature so future enhancements don't need a churn).
func runStatus(ctx context.Context, stdout, _ io.Writer, format string) error {
	out, err := collectStatus(ctx)
	if err != nil {
		return err
	}

	switch format {
	case output.JSON:
		return writeJSON(stdout, out)
	case output.YAML:
		return writeYAML(stdout, out)
	default:
		// Table / text / human / unset all share the bespoke sectioned
		// layout: the data here is not row-shaped, so the generic
		// table renderer would be a poor fit. Unknown format strings
		// also land here rather than erroring — kit's --format flag
		// is validated upstream, and a defensive fallback beats a
		// surprise nil-output for callers that wire status manually.
		return writeHuman(stdout, out)
	}
}

// collectStatus assembles a StatusOutput from the three data sources.
// Errors from optional reads (install_id read, install_id path
// resolve) are folded into the payload so the rest of the report
// still renders — a partial status is more useful than no status.
// Errors from required reads (consent store construction, consent
// store Get) surface as the function's error return because they
// indicate a misconfigured environment that the operator must fix
// before any other telemetry command will work.
func collectStatus(ctx context.Context) (StatusOutput, error) {
	out := StatusOutput{}

	store, err := consent.NewFileStore()
	if err != nil {
		return out, fmt.Errorf("status: cannot open consent store: %w", err)
	}
	decision, err := store.Get(ctx)
	if err != nil {
		return out, fmt.Errorf("status: cannot read consent: %w", err)
	}
	out.Consent = consentInfo(decision)

	out.Identity = identityInfo()
	out.Mode = modeInfo()
	return out, nil
}

// consentInfo flattens a Decision into the wire-shape payload.
// StateUnknown emits an empty decided_at + empty decision_source so
// JSON consumers see a clear "no decision yet" shape rather than a
// zero-time string that looks like a real date.
func consentInfo(d consent.Decision) ConsentInfo {
	info := ConsentInfo{
		State:          string(d.State),
		PromptVersion:  d.PromptVersion,
		DecisionSource: string(d.DecisionSource),
	}
	if !d.DecidedAt.IsZero() {
		info.DecidedAt = d.DecidedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return info
}

// identityInfo reads the anonymous installation id and its on-disk
// path. Either read may fail (first-run failure to create the state
// dir, permission denial, etc.); the failure mode is folded into the
// string fields with an "(error: ...)" sentinel so the renderer can
// still produce a complete report.
func identityInfo() IdentityInfo {
	info := IdentityInfo{}

	id, err := runtimetel.InstallationID()
	if err != nil {
		info.InstallationID = "(error: " + err.Error() + ")"
	} else {
		info.InstallationID = id
	}

	path, err := runtimetel.InstallIDPath()
	if err != nil {
		info.Path = "(error: " + err.Error() + ")"
	} else {
		info.Path = path
	}
	return info
}

// modeInfo snapshots the package-global telemetry Mode and the
// registered application prefix. Both are atomic reads; no I/O.
func modeInfo() ModeInfo {
	return ModeInfo{
		Current:   runtimetel.CurrentMode().String(),
		AppPrefix: runtimetel.CurrentAppPrefix(),
	}
}

// writeJSON emits StatusOutput as indented JSON with a trailing
// newline. Indentation matches kitinit.WriteJSON so all kit JSON
// renders are diff-friendly under the same conventions.
func writeJSON(w io.Writer, s StatusOutput) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// writeYAML emits StatusOutput as YAML. Uses yaml.v3 directly (no
// custom node manipulation) — the payload is plain-data, so default
// marshaling is correct and stable.
func writeYAML(w io.Writer, s StatusOutput) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(s); err != nil {
		_ = enc.Close()
		return err
	}
	return enc.Close()
}

// writeHuman renders the sectioned terminal-friendly layout. Field
// labels are right-padded to a fixed width within each section so
// values align in a column, mirroring the `kit status` and `kit
// config show` operator-facing renders. Empty optional fields render
// as "(none)" rather than blank — operators reading the output should
// not have to guess whether a missing line means "absent" or "bug".
func writeHuman(w io.Writer, s StatusOutput) error {
	var b strings.Builder

	b.WriteString("Consent:\n")
	b.WriteString(humanRow("State", orNone(s.Consent.State)))
	b.WriteString(humanRow("Decided at", orNone(s.Consent.DecidedAt)))
	b.WriteString(humanRow("Prompt version", fmt.Sprintf("%d", s.Consent.PromptVersion)))
	b.WriteString(humanRow("Source", orNone(s.Consent.DecisionSource)))
	b.WriteString("\n")

	b.WriteString("Identity:\n")
	b.WriteString(humanRow("Install ID", orNone(s.Identity.InstallationID)))
	b.WriteString(humanRow("Path", orNone(s.Identity.Path)))
	b.WriteString("\n")

	b.WriteString("Mode:\n")
	b.WriteString(humanRow("Current", orNone(s.Mode.Current)))
	b.WriteString(humanRow("App prefix", orNone(s.Mode.AppPrefix)))

	_, err := io.WriteString(w, b.String())
	return err
}

// humanLabelWidth is the column width to which field labels are
// padded inside writeHuman. 16 chars covers "Prompt version" (14)
// with two trailing spaces of breathing room; bumping this only
// changes the visual gap, so we pick a value that survives the
// longest current label without rewriting every test.
const humanLabelWidth = 16

// humanRow formats one "  Label:           Value\n" line. Indents two
// spaces under the section header, pads the label to humanLabelWidth,
// then a single space before the value. Trailing newline is required;
// callers do not append one themselves.
func humanRow(label, value string) string {
	return fmt.Sprintf("  %-*s %s\n", humanLabelWidth, label+":", value)
}

// orNone substitutes "(none)" for an empty string. The human render
// uses this for every optional field so operators see an explicit
// "no value" rather than wondering whether the line was elided.
func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
