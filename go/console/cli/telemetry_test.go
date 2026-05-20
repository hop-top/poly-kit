package cli

import (
	"testing"

	runtimetelemetry "hop.top/kit/go/runtime/telemetry"
)

// TestWithTelemetry_StashesConfig verifies the option round-trips
// through the Root accessor. Mirrors the WithIdentity tests' shape:
// stash via option, read via accessor, assert value parity.
func TestWithTelemetry_StashesConfig(t *testing.T) {
	cfg := TelemetryConfig{
		Endpoint:           "https://collector.example/v1",
		PromptOnFirstRun:   true,
		DefaultModeOnGrant: runtimetelemetry.ModeAnon,
	}

	r := &Root{}
	WithTelemetry(cfg)(r)

	if !r.HasTelemetry() {
		t.Fatal("HasTelemetry returned false after WithTelemetry call")
	}
	got := r.Telemetry()
	if got != cfg {
		t.Fatalf("Telemetry() = %+v; want %+v", got, cfg)
	}
}

// TestTelemetry_ZeroValueWhenUnset documents the contract for
// adopters that never call WithTelemetry: Telemetry() returns the
// zero value (no panic, no nil deref) and HasTelemetry reports
// false. The zero value is meaningful — PromptOnFirstRun=false +
// DefaultModeOnGrant=ModeOff is the safe "do nothing" posture.
func TestTelemetry_ZeroValueWhenUnset(t *testing.T) {
	r := &Root{}
	if r.HasTelemetry() {
		t.Fatal("HasTelemetry returned true on a freshly constructed Root")
	}
	got := r.Telemetry()
	if (got != TelemetryConfig{}) {
		t.Fatalf("Telemetry() on unconfigured Root = %+v; want zero value", got)
	}
}

// TestWithTelemetry_OverwritesPriorCall mirrors WithIdentity's
// idempotency contract: the last call wins. Adopters that build
// their Config dynamically need this; tests that rely on it should
// pass.
func TestWithTelemetry_OverwritesPriorCall(t *testing.T) {
	r := &Root{}
	WithTelemetry(TelemetryConfig{Endpoint: "https://first.example/v1"})(r)
	WithTelemetry(TelemetryConfig{
		Endpoint:           "https://second.example/v1",
		PromptOnFirstRun:   true,
		DefaultModeOnGrant: runtimetelemetry.ModeFull,
	})(r)

	got := r.Telemetry()
	if got.Endpoint != "https://second.example/v1" {
		t.Fatalf("Endpoint = %q; want https://second.example/v1", got.Endpoint)
	}
	if !got.PromptOnFirstRun {
		t.Fatal("PromptOnFirstRun = false; want true (second call set it)")
	}
	if got.DefaultModeOnGrant != runtimetelemetry.ModeFull {
		t.Fatalf("DefaultModeOnGrant = %v; want ModeFull", got.DefaultModeOnGrant)
	}
}
