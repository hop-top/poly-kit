package ext_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hop.top/kit/go/ai/ext"
)

// stubExt is a test extension with configurable capabilities.
type stubExt struct {
	name  string
	caps  ext.Capability
	initE error
	clsE  error
}

func (s *stubExt) Meta() ext.Metadata           { return ext.Metadata{Name: s.name, Version: "1.0.0"} }
func (s *stubExt) Capabilities() ext.Capability { return s.caps }
func (s *stubExt) Init(_ context.Context) error { return s.initE }
func (s *stubExt) Close() error                 { return s.clsE }

func TestManager_Add_RoutesToCallbacks(t *testing.T) {
	m := ext.NewManager(nil)

	var gotReg, gotHook, gotDisc, gotCfg bool
	m.SetOnRegistry(func(_ ext.Extension) { gotReg = true })
	m.SetOnHook(func(_ ext.Extension) { gotHook = true })
	m.SetOnDiscover(func(_ ext.Extension) { gotDisc = true })
	m.SetOnConfig(func(_ ext.Extension) { gotCfg = true })

	e := &stubExt{
		name: "multi",
		caps: ext.CapRegistry | ext.CapHook | ext.CapDiscover | ext.CapConfig,
	}
	m.Add(e)

	if !gotReg {
		t.Error("expected registry callback")
	}
	if !gotHook {
		t.Error("expected hook callback")
	}
	if !gotDisc {
		t.Error("expected discover callback")
	}
	if !gotCfg {
		t.Error("expected config callback")
	}
}

func TestManager_Add_SkipsMissingCallbacks(t *testing.T) {
	m := ext.NewManager(nil)
	// No callbacks set — should not panic.
	m.Add(&stubExt{name: "safe", caps: ext.CapRegistry | ext.CapHook})
}

func TestManager_Add_SingleCapability(t *testing.T) {
	m := ext.NewManager(nil)

	var gotReg, gotHook bool
	m.SetOnRegistry(func(_ ext.Extension) { gotReg = true })
	m.SetOnHook(func(_ ext.Extension) { gotHook = true })

	m.Add(&stubExt{name: "reg-only", caps: ext.CapRegistry})

	if !gotReg {
		t.Error("expected registry callback for CapRegistry")
	}
	if gotHook {
		t.Error("hook callback should not fire for CapRegistry-only extension")
	}
}

func TestManager_InitAll(t *testing.T) {
	m := ext.NewManager(nil)
	m.Add(&stubExt{name: "a", caps: ext.CapRegistry})
	m.Add(&stubExt{name: "b", caps: ext.CapRegistry})

	if err := m.InitAll(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManager_InitAll_StopsOnError(t *testing.T) {
	m := ext.NewManager(nil)
	boom := errors.New("boom")
	m.Add(&stubExt{name: "ok", caps: ext.CapRegistry})
	m.Add(&stubExt{name: "fail", caps: ext.CapRegistry, initE: boom})
	m.Add(&stubExt{name: "never", caps: ext.CapRegistry})

	err := m.InitAll(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, boom) {
		t.Errorf("expected wrapped boom, got: %v", err)
	}
	if got := err.Error(); got == "" || !contains(got, "fail") {
		t.Errorf("error should mention failing extension name, got: %s", got)
	}
}

func TestManager_CloseAll_ReverseOrder(t *testing.T) {
	m := ext.NewManager(nil)

	var order []string
	m.Add(&closeSpy{name: "first", order: &order})
	m.Add(&closeSpy{name: "second", order: &order})
	m.Add(&closeSpy{name: "third", order: &order})

	errs := m.CloseAll()
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(order) != 3 || order[0] != "third" || order[1] != "second" || order[2] != "first" {
		t.Errorf("expected reverse order [third second first], got %v", order)
	}
}

func TestManager_Extensions(t *testing.T) {
	m := ext.NewManager(nil)
	m.Add(&stubExt{name: "a", caps: ext.CapRegistry})
	m.Add(&stubExt{name: "b", caps: ext.CapHook})

	exts := m.Extensions()
	if len(exts) != 2 {
		t.Fatalf("expected 2 extensions, got %d", len(exts))
	}
}

type closeSpy struct {
	name  string
	order *[]string
}

func (c *closeSpy) Meta() ext.Metadata           { return ext.Metadata{Name: c.name} }
func (c *closeSpy) Capabilities() ext.Capability { return ext.CapRegistry }
func (c *closeSpy) Init(_ context.Context) error { return nil }
func (c *closeSpy) Close() error {
	*c.order = append(*c.order, c.name)
	return nil
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
