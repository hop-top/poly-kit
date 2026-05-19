// Telemetry-wiring e2e for the spaced adopter integration (T-0695).
//
// Asserts that running spaced with --telemetry=anon and a granted
// consent file publishes one event on the bus topic
// `spaced.telemetry.event.recorded` — proving the adopter prefix +
// per-context mode override + post-run record path is wired end-to-end.
//
// In-process by design: spaced's bus is per-process and there is no
// env-driven sink redirection in the bus package today. T-0695 spec
// (last bullet) explicitly authorizes whatever in-test capture
// mechanism is cleanest, so we reuse the same wiring helpers main()
// calls and subscribe synchronously on a memory bus.
package main

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/examples/spaced/go/cmd"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/telemetry"
)

// TestTelemetryWiring_AnonModeEmitsOneEvent verifies the full adopter
// wire: the persistent --telemetry=anon flag, the consent gate, the
// post-run Record call, and the adopter topic prefix.
//
// What the test pins down:
//
//   - Topic equals "spaced.telemetry.event.recorded" (the
//     WithTopicPrefix override applied; the kit default is
//     "kit.telemetry.event.recorded").
//   - Exactly one event is observed for one cobra invocation (no
//     double-record from PreRun + PostRun).
//   - The event payload carries a non-empty installation_id and a
//     non-empty command_path — the two cross-language schema fields
//     adopters rely on.
func TestTelemetryWiring_AnonModeEmitsOneEvent(t *testing.T) {
	// Sandbox XDG so the test never touches the developer's real
	// telemetry.yaml or installation_id file. xdg.{Config,State}File
	// honors XDG_*_HOME, see go/core/xdg/xdg_test.go.
	xdgRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(xdgRoot, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(xdgRoot, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(xdgRoot, "data"))

	// Seed a granted consent decision so the emitter's consent gate
	// passes. The on-disk format is owned by go/core/consent
	// (telemetry.yaml under XDG_CONFIG_HOME/kit). Minimal valid doc
	// per ADR-0036: state + decided_at + prompt_version +
	// decision_source.
	cfgDir := filepath.Join(xdgRoot, "config", "kit")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir consent dir: %v", err)
	}
	consentYAML := "" +
		"telemetry:\n" +
		"  consent:\n" +
		"    state: granted\n" +
		"    decided_at: " + time.Now().UTC().Format(time.RFC3339) + "\n" +
		"    prompt_version: 1\n" +
		"    decision_source: prompt\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "telemetry.yaml"), []byte(consentYAML), 0o600); err != nil {
		t.Fatalf("write consent yaml: %v", err)
	}

	// Independent bus so the test owns subscription lifecycle and
	// the package-global emitter state from previous tests can't
	// leak in.
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()

	// Synchronous subscriber — Publish returns after the handler
	// runs (see go/runtime/bus/bus.go memBus.Publish), so the
	// assertion below races nothing.
	var count atomic.Int32
	var lastTopic atomic.Value // bus.Topic
	var lastEvent atomic.Value // telemetry.Event
	b.Subscribe("spaced.telemetry.event.#", func(_ context.Context, e bus.Event) error {
		count.Add(1)
		lastTopic.Store(e.Topic)
		if ev, ok := e.Payload.(telemetry.Event); ok {
			lastEvent.Store(ev)
		}
		return nil
	})

	// Wire telemetry against this bus. initTelemetry calls
	// SetMode(ModeOff) explicitly; the --telemetry=anon flag below
	// overrides per-invocation via WithMode (precedence #1).
	initTelemetry(b)
	if spacedTelemetryEmitter == nil {
		t.Fatal("initTelemetry left spacedTelemetryEmitter nil; emitter construction failed")
	}
	t.Cleanup(func() { spacedTelemetryEmitter = nil })

	// Build a minimal cobra root that mirrors main() — same Config
	// (so the kit PersistentPreRunE chain is identical), same flag,
	// same Pre/PostRun hooks. We don't reuse the production main()
	// because Execute calls os.Exit on validation failure; the
	// integration here exercises the wiring, not the binary's exit
	// behavior.
	root := cli.New(cli.Config{
		Name:             "spaced",
		Version:          "0.1.0",
		Short:            "spaced telemetry e2e",
		Accent:           "#FF5733",
		MaxTopLevelVerbs: 12,
		Hooks:            cli.Hooks{PrePersistentRunE: installTelemetryPreRunHook},
	})
	root.Cmd.PersistentFlags().String("telemetry", "off", "kit-telemetry emit mode (off|anon|full)")
	root.Cmd.PersistentPostRunE = installTelemetryPostRun

	// A trivial child command so cobra has a runnable leaf — using
	// real spaced commands (launch, mission) pulls in data/network
	// concerns irrelevant to the wiring assertion. The PostRunE
	// fires at the leaf regardless of which leaf runs.
	leaf := &cobra.Command{
		Use:   "ping",
		Short: "no-op leaf for telemetry wiring assertion",
		RunE:  func(_ *cobra.Command, _ []string) error { return nil },
	}
	root.Cmd.AddCommand(leaf)

	// Also wire one real command so the test name "launch publishes
	// one event" stays true to the plan's wording. We pass --dry-run
	// to keep launch side-effect-free.
	root.Cmd.AddCommand(cmd.LaunchCmd(root, b))

	// Bypass the kit Layer-A validator. The validator demands
	// kit/side-effect annotations on every leaf; the wiring under
	// test runs upstream of that check.
	root.Config.DisableValidate = true
	root.Config.EnforceValidate = false

	// Exec: spaced --telemetry=anon launch --dry-run starman
	root.Cmd.SetArgs([]string{"--telemetry=anon", "launch", "--dry-run", "starman"})
	if err := root.Execute(context.Background()); err != nil {
		t.Fatalf("root.Execute: %v", err)
	}

	// Assertions.
	if got := count.Load(); got != 1 {
		t.Fatalf("expected exactly 1 telemetry event, got %d", got)
	}

	gotTopicRaw := lastTopic.Load()
	if gotTopicRaw == nil {
		t.Fatal("no topic captured")
	}
	gotTopic, _ := gotTopicRaw.(bus.Topic)
	wantTopic := bus.Topic("spaced.telemetry.event.recorded")
	if gotTopic != wantTopic {
		t.Errorf("topic: got %q, want %q", gotTopic, wantTopic)
	}

	evRaw := lastEvent.Load()
	if evRaw == nil {
		t.Fatal("event payload not telemetry.Event")
	}
	ev, _ := evRaw.(telemetry.Event)
	if ev.InstallationID == "" {
		t.Error("event.InstallationID is empty; emitter did not stamp install_id")
	}
	if len(ev.CommandPath) == 0 {
		t.Error("event.CommandPath is empty")
	}
	if ev.Mode != "anon" {
		t.Errorf("event.Mode: got %q, want %q", ev.Mode, "anon")
	}
	// Anon tier MUST strip Args/Flags (ADR-0035 #6).
	if len(ev.Args) != 0 {
		t.Errorf("event.Args should be empty in anon mode, got %v", ev.Args)
	}
	if len(ev.Flags) != 0 {
		t.Errorf("event.Flags should be empty in anon mode, got %v", ev.Flags)
	}
}
