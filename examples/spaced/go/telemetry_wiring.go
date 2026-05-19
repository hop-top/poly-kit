// Telemetry wiring for the spaced demo. Demonstrates the canonical
// kit-telemetry adopter pattern:
//
//  1. SetAppPrefix("spaced")  -> reads SPACED_TELEMETRY_MODE env var.
//  2. SetMode(ModeOff)        -> documents the default explicitly.
//  3. SetConsentHook(...)     -> consent-gated emit (default-deny).
//  4. emitter via WithBus + WithTopicPrefix("spaced.telemetry.event").
//  5. --telemetry={off,anon,full} persistent flag -> WithMode on ctx.
//  6. PersistentPreRunE  records start time on ctx.
//  7. PersistentPostRunE calls Emitter.Record with command path +
//     exit code + duration.
//
// Kept distinct from spaced's own `telemetry` subcommand (which serves
// mission telemetry streams — separate concern from kit runtime
// telemetry).
package main

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/core/consent"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/telemetry"
)

// spacedKitVersion is stamped on every emitted Event's kit_version
// field. Build systems can override via -ldflags '-X main.spacedKitVersion=...'.
var spacedKitVersion = "0.1.0"

// spacedTelemetryEmitter is the package-global emitter wired by
// initTelemetry. May be nil if construction failed (e.g. missing
// redactor in ModeFull); callers MUST nil-check before Record.
var spacedTelemetryEmitter *telemetry.Emitter

// startTimeCtxKey carries the PersistentPreRunE start time forward to
// PersistentPostRunE so the latter can compute duration_ms without
// reaching back into the cobra command tree.
type startTimeCtxKey struct{}

// withStartTime stamps now onto ctx. Used by the root PersistentPreRunE.
func withStartTime(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, startTimeCtxKey{}, t)
}

// startTimeFromContext returns the timestamp stamped by withStartTime,
// or the zero time if none was set (in which case duration is reported
// as 0).
func startTimeFromContext(ctx context.Context) time.Time {
	if v, ok := ctx.Value(startTimeCtxKey{}).(time.Time); ok {
		return v
	}
	return time.Time{}
}

// initTelemetry wires the kit-telemetry emitter against the supplied
// bus. Idempotent across calls — only the last invocation is in effect.
//
// The bus argument is shared with the rest of spaced (see main.go) so
// telemetry events appear on the same bus as launch/daemon events,
// which keeps the demo's subscriber set unified.
func initTelemetry(b bus.Bus) {
	// SPACED_TELEMETRY_MODE wins over KIT_TELEMETRY_MODE.
	telemetry.SetAppPrefix("spaced")

	// Explicit ModeOff — no-op but documents the default and short-
	// circuits the env-precedence read. Removed once kit-consent ships
	// the full prompt UX and adopters lean on env/flag overrides only.
	telemetry.SetMode(telemetry.ModeOff)

	// Consent hook wiring. Default-deny lives in the package; this
	// installs the file-backed store explicitly so the consent file
	// path (XDG_CONFIG_HOME/kit/config.yaml under
	// kit.telemetry.consent) is the gating surface.
	if store, err := consent.NewFileStore(); err == nil {
		telemetry.SetConsentHook(consent.NewHook(store))
	}

	// Only load the redactor when ModeFull is possible. Anon/Off paths
	// never touch the redactor; refusing to load it on Off saves the
	// gitleaks+Presidio corpus parse on every invocation.
	var redactor *redact.Redactor
	if telemetry.CurrentMode() == telemetry.ModeFull {
		redactor = telemetry.MustLoadRedactor()
	}

	emitter, err := telemetry.New(
		telemetry.WithBus(b),
		telemetry.WithRedactor(redactor),
		telemetry.WithTopicPrefix("spaced.telemetry.event"),
		telemetry.WithKitVersion(spacedKitVersion),
	)
	if err != nil {
		// Soft refusal: an emitter that won't construct must not
		// crash the host program. The wiring stays nil; PostRunE
		// short-circuits.
		return
	}
	spacedTelemetryEmitter = emitter
}

// installTelemetryFlag adds the --telemetry={off,anon,full} persistent
// flag onto root and composes its parse step ahead of the kit
// PersistentPreRunE chain. The flag wins over both SPACED_TELEMETRY_MODE
// and KIT_TELEMETRY_MODE because WithMode is a per-context override.
//
// This is also where the start-time stamp lands on the ctx so
// PersistentPostRunE can compute duration_ms.
func installTelemetryPreRunHook(cmd *cobra.Command, args []string) error {
	if raw, _ := cmd.Flags().GetString("telemetry"); raw != "" {
		if m, ok := telemetry.ParseMode(raw); ok {
			cmd.SetContext(telemetry.WithMode(cmd.Context(), m))
		}
	}
	cmd.SetContext(withStartTime(cmd.Context(), time.Now()))
	_ = args
	return nil
}

// installTelemetryPostRun emits a single telemetry event at command
// completion. Soft-refuses (no Record call) when the emitter wasn't
// constructed. ExitCode is reported as 0 here — full exit-code capture
// requires bubbling RunE's error up; that is a follow-up so spaced
// records the happy-path metric for now.
func installTelemetryPostRun(cmd *cobra.Command, _ []string) error {
	if spacedTelemetryEmitter == nil {
		return nil
	}
	ctx := cmd.Context()
	var durMS int64
	if start := startTimeFromContext(ctx); !start.IsZero() {
		durMS = time.Since(start).Milliseconds()
	}
	return spacedTelemetryEmitter.Record(ctx, telemetry.Event{
		CommandPath: strings.Split(cmd.CommandPath(), " "),
		ExitCode:    0,
		DurationMS:  durMS,
	})
}
