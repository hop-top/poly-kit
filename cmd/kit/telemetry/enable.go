// enable.go implements `kit telemetry enable`: persists an affirmative
// consent decision (StateGranted, SourceFlag) so subsequent telemetry
// events ship under the per-batch consent hook.
//
// The command is defensive about the precedence chain (ADR-0036 §5).
// If an env kill switch (DO_NOT_TRACK=1 or *_TELEMETRY_MODE=off) is set
// at invocation time, the resolver would mask any granted state we
// write. Rather than silently disagree with what the operator's shell
// is asking for, we refuse the write and explain which env var is in
// the way — the operator unsets it, re-runs, and gets the unambiguous
// "you are now opted in" result they asked for.
//
// Honors the kit-wide --dry-run via cli.IsDryRun: when true, prints
// what WOULD be written and returns without touching the store.

package telemetry

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/consent"
	runtimetel "hop.top/kit/go/runtime/telemetry"
)

// enableCmd builds the `kit telemetry enable` leaf. RunE delegates to
// runEnable so the body stays exercisable from tests without spinning a
// cobra invocation. The Long copy names the env conflicts explicitly so
// operators reading `--help` know up front why an enable may refuse.
func enableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable",
		Short: "Grant consent for kit anonymous usage telemetry",
		Long: `Grant consent to send anonymous telemetry. Persists the decision
with decision_source=flag.

The next telemetry event after this command will respect the new
grant via the per-batch consent hook in the emitter.

Refuses with a clear error when DO_NOT_TRACK=1 or *_TELEMETRY_MODE=off
(app-prefixed or kit-prefixed) — the precedence chain would silently
override the write, so we surface the conflict instead.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runEnable(cmd.Context(), cmd.OutOrStdout(), cli.IsDryRun(cmd))
		},
	}
}

// runEnable is the testable core. dryRun=true short-circuits before any
// store mutation so the env-block precedence check still runs but the
// "would write" path produces no side effects.
func runEnable(ctx context.Context, stdout io.Writer, dryRun bool) error {
	// 1. Env kill switches FIRST. Writing a granted state that the
	//    resolver would immediately mask is worse than a clear refusal:
	//    the operator would think they opted in and quietly emit nothing.
	if envName, blocked := consentEnvBlocked(); blocked {
		return fmt.Errorf("telemetry enable refused: %s blocks consent grants. Unset it and re-run", envName)
	}

	d := consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      time.Now().UTC(),
		PromptVersion:  PromptVersion,
		DecisionSource: consent.SourceFlag,
	}

	if dryRun {
		_, _ = fmt.Fprintln(stdout, "dry-run: would persist consent state=granted source=flag")
		return nil
	}

	store, err := consent.NewFileStore()
	if err != nil {
		return fmt.Errorf("enable: open consent store: %w", err)
	}
	if err := store.Set(ctx, d); err != nil {
		return fmt.Errorf("enable: persist decision: %w", err)
	}

	_, _ = fmt.Fprintln(stdout, "Telemetry enabled. Run `kit telemetry status` to verify.")
	return nil
}

// consentEnvBlocked returns (envName, true) when the current env would
// mask a granted consent. App-prefix env var is checked BEFORE the kit-
// prefix per ADR-0035 — the embedding app's switch takes precedence
// over kit's default. DO_NOT_TRACK is checked first as the cross-tool
// industry convention (https://consoledonottrack.com/); we honor any
// non-empty value other than "0"/"false" via consent.DoNotTrackEnabled.
func consentEnvBlocked() (string, bool) {
	if consent.DoNotTrackEnabled(consent.OSEnv()) {
		return "DO_NOT_TRACK", true
	}
	if appPrefix := runtimetel.CurrentAppPrefix(); appPrefix != "" {
		varName := strings.ToUpper(appPrefix) + "_TELEMETRY_MODE"
		if strings.EqualFold(os.Getenv(varName), "off") {
			return varName + "=off", true
		}
	}
	if strings.EqualFold(os.Getenv("KIT_TELEMETRY_MODE"), "off") {
		return "KIT_TELEMETRY_MODE=off", true
	}
	return "", false
}
