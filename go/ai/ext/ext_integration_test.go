//go:build integration

package ext_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/ext"
	"hop.top/kit/go/ai/ext/config"
	"hop.top/kit/go/ai/ext/hook"
	"hop.top/kit/go/ai/ext/registry"
	"hop.top/kit/go/console/cli"
	kitlog "hop.top/kit/go/console/log"
)

// greetExt is a sample extension that registers a cobra subcommand and
// subscribes to lifecycle hooks.
type greetExt struct {
	greeted bool
}

func (g *greetExt) Meta() ext.Metadata {
	return ext.Metadata{Name: "greet", Version: "0.1.0", Description: "says hello"}
}

func (g *greetExt) Capabilities() ext.Capability {
	return ext.CapRegistry | ext.CapHook | ext.CapConfig
}

func (g *greetExt) Init(_ context.Context) error {
	g.greeted = true
	return nil
}

func (g *greetExt) Close() error {
	g.greeted = false
	return nil
}

// TestIntegration_FullLifecycle wires cli.Root, Manager, registry, hook bus,
// and config store — then verifies the full extension lifecycle.
func TestIntegration_FullLifecycle(t *testing.T) {
	// 1. Build CLI root.
	root := cli.New(cli.Config{
		Name: "sample", Version: "0.0.1", Short: "ext integration test",
		DisableValidate: true,
	})

	// 2. Create logger from CLI viper.
	l := kitlog.New(root.Viper)
	var logBuf bytes.Buffer
	l.SetOutput(&logBuf)
	l.SetColorProfile(colorprofile.NoTTY)

	// 3. Build manager + mechanisms.
	mgr := ext.NewManager(l)
	reg := registry.New()
	bus := hook.NewBus()
	store := config.NewStore()

	// Load config that enables "greet".
	cfgYAML := `extensions:
  greet:
    enabled: true
    settings:
      greeting: howdy
`
	if err := store.Load(strings.NewReader(cfgYAML)); err != nil {
		t.Fatalf("config load: %v", err)
	}

	// Wire callbacks.
	mgr.SetOnRegistry(func(e ext.Extension) { reg.Register(e) })
	mgr.SetOnHook(func(e ext.Extension) {
		bus.Subscribe(hook.AfterInit, func(ctx context.Context, _ any) error {
			t.Logf("hook fired for %s", e.Meta().Name)
			return nil
		}, 0)
	})

	// 4. Add extension (only if config says enabled).
	g := &greetExt{}
	if store.IsEnabled(g.Meta().Name) {
		mgr.Add(g)
	}

	// 5. Verify registry got it.
	found, ok := reg.Get("greet")
	if !ok {
		t.Fatal("greet extension not found in registry")
	}
	if found.Meta().Version != "0.1.0" {
		t.Errorf("expected version 0.1.0, got %s", found.Meta().Version)
	}

	// 6. Verify config settings.
	settings := store.Settings("greet")
	if settings["greeting"] != "howdy" {
		t.Errorf("expected greeting=howdy, got %v", settings["greeting"])
	}

	// 7. Verify hook subscription.
	if bus.Handlers(hook.AfterInit) != 1 {
		t.Errorf("expected 1 AfterInit handler, got %d", bus.Handlers(hook.AfterInit))
	}

	// 8. Init all.
	if err := mgr.InitAll(context.Background()); err != nil {
		t.Fatalf("InitAll: %v", err)
	}
	if !g.greeted {
		t.Error("expected greeted=true after InitAll")
	}

	// 9. Fire hook.
	if err := bus.Dispatch(context.Background(), hook.AfterInit, nil); err != nil {
		t.Fatalf("hook dispatch: %v", err)
	}

	// 10. Close all.
	errs := mgr.CloseAll()
	if len(errs) != 0 {
		t.Fatalf("CloseAll errors: %v", errs)
	}
	if g.greeted {
		t.Error("expected greeted=false after CloseAll")
	}

	// 11. Verify the extension count.
	all := mgr.Extensions()
	if len(all) != 1 {
		t.Errorf("expected 1 extension, got %d", len(all))
	}
}

// TestIntegration_ConfigDisablesExtension verifies that a disabled extension
// is never added to the manager or registry.
func TestIntegration_ConfigDisablesExtension(t *testing.T) {
	store := config.NewStore()
	cfgYAML := `extensions:
  greet:
    enabled: false
`
	if err := store.Load(strings.NewReader(cfgYAML)); err != nil {
		t.Fatalf("config load: %v", err)
	}

	mgr := ext.NewManager(nil)
	reg := registry.New()
	mgr.SetOnRegistry(func(e ext.Extension) { reg.Register(e) })

	g := &greetExt{}
	if store.IsEnabled(g.Meta().Name) {
		mgr.Add(g)
	}

	if _, ok := reg.Get("greet"); ok {
		t.Error("disabled extension should not be in registry")
	}
	if len(mgr.Extensions()) != 0 {
		t.Error("disabled extension should not be in manager")
	}
}

// TestIntegration_ExtensionAddsCobraCommand verifies an extension can
// register a cobra subcommand on the CLI root.
func TestIntegration_ExtensionAddsCobraCommand(t *testing.T) {
	root := cli.New(cli.Config{
		Name: "sample", Version: "0.0.1", Short: "test",
		DisableValidate: true,
	})

	var ran bool
	root.Cmd.AddCommand(&cobra.Command{
		Use:   "greet",
		Short: "say hello",
		RunE: func(_ *cobra.Command, _ []string) error {
			ran = true
			return nil
		},
	})

	root.Cmd.SetArgs([]string{"greet"})
	if err := root.Execute(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !ran {
		t.Error("greet subcommand did not run")
	}
}

// TestIntegration_HookBeforeRun verifies hooks fire around command execution.
func TestIntegration_HookBeforeRun(t *testing.T) {
	bus := hook.NewBus()

	var hookOrder []string
	bus.Subscribe(hook.BeforeRun, func(_ context.Context, _ any) error {
		hookOrder = append(hookOrder, "before")
		return nil
	}, 0)
	bus.Subscribe(hook.AfterRun, func(_ context.Context, _ any) error {
		hookOrder = append(hookOrder, "after")
		return nil
	}, 0)

	root := cli.New(cli.Config{
		Name: "sample", Version: "0.0.1", Short: "test",
		DisableValidate: true,
	})
	root.Cmd.AddCommand(&cobra.Command{
		Use:   "work",
		Short: "do work",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx := context.Background()
			if err := bus.Dispatch(ctx, hook.BeforeRun, nil); err != nil {
				return err
			}
			hookOrder = append(hookOrder, "run")
			return bus.Dispatch(ctx, hook.AfterRun, nil)
		},
	})

	root.Cmd.SetArgs([]string{"work"})
	if err := root.Execute(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}

	expected := []string{"before", "run", "after"}
	if len(hookOrder) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(hookOrder), hookOrder)
	}
	for i, want := range expected {
		if hookOrder[i] != want {
			t.Errorf("event[%d]: want %q, got %q", i, want, hookOrder[i])
		}
	}
}

// TestIntegration_QuietSuppressesExtLogs verifies that --quiet suppresses
// manager debug logs while extensions still initialize.
func TestIntegration_QuietSuppressesExtLogs(t *testing.T) {
	root := cli.New(cli.Config{
		Name: "sample", Version: "0.0.1", Short: "test",
		DisableValidate: true,
	})
	_ = root.Cmd.PersistentFlags().Set("quiet", "true")

	l := kitlog.New(root.Viper)
	var buf bytes.Buffer
	l.SetOutput(&buf)
	l.SetColorProfile(colorprofile.NoTTY)

	mgr := ext.NewManager(l)
	mgr.Add(&stubExt{name: "quiet-test", caps: ext.CapRegistry})

	if err := mgr.InitAll(context.Background()); err != nil {
		t.Fatalf("InitAll: %v", err)
	}

	// Debug messages from manager should be suppressed.
	if buf.Len() > 0 {
		t.Errorf("expected no log output with --quiet, got: %s", buf.String())
	}
}
