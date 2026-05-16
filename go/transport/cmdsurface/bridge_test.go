package cmdsurface

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/spf13/cobra"
)

// newBridgeTree builds a small tree with a mix of safe and
// destructive leaves used by the bridge tests:
//
//	root
//	├── widget
//	│   ├── add        (write)
//	│   └── delete     (destructive)
//	├── report
//	│   └── daily      (read)
//	└── ping           (read)
func newBridgeTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	widget := &cobra.Command{Use: "widget"}
	add := &cobra.Command{
		Use: "add",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("added")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	del := &cobra.Command{
		Use: "delete",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("deleted")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	widget.AddCommand(add)
	widget.AddCommand(del)
	root.AddCommand(widget)

	report := &cobra.Command{Use: "report"}
	daily := &cobra.Command{
		Use:         "daily",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	report.AddCommand(daily)
	root.AddCommand(report)

	ping := &cobra.Command{
		Use:         "ping",
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Println("pong"); return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)

	return root
}

func TestBridge_Leaves_Discovery(t *testing.T) {
	b := New(newBridgeTree())
	got := leafKeys(b.Leaves())
	want := []string{"ping", "report daily", "widget add", "widget delete"}
	if !sameSorted(got, want) {
		t.Fatalf("leaves=%v want=%v", got, want)
	}
}

func TestBridge_Leaves_DefaultEnabled(t *testing.T) {
	b := New(newBridgeTree())
	for _, l := range b.Leaves() {
		for _, def := range []Surface{SurfaceCLI, SurfaceLib, SurfaceMCP} {
			if !l.Enabled[def] {
				t.Errorf("leaf %q missing default surface %s", l.PathKey(), def)
			}
		}
		if l.Enabled[SurfaceREST] {
			t.Errorf("leaf %q REST should default off", l.PathKey())
		}
	}
}

func TestBridge_Expose_Exact(t *testing.T) {
	b := New(newBridgeTree()).Expose("widget add", SurfaceREST)
	for _, l := range b.Leaves() {
		got := l.Enabled[SurfaceREST]
		want := l.PathKey() == "widget add"
		if got != want {
			t.Errorf("REST on %q: got=%v want=%v", l.PathKey(), got, want)
		}
	}
}

func TestBridge_Expose_Subtree(t *testing.T) {
	b := New(newBridgeTree()).Expose("widget *", SurfaceREST)
	for _, l := range b.Leaves() {
		got := l.Enabled[SurfaceREST]
		want := len(l.Path) >= 1 && l.Path[0] == "widget"
		if got != want {
			t.Errorf("REST on %q: got=%v want=%v", l.PathKey(), got, want)
		}
	}
}

func TestBridge_Expose_Wildcard(t *testing.T) {
	b := New(newBridgeTree()).Expose("*", SurfaceREST)
	for _, l := range b.Leaves() {
		if !l.Enabled[SurfaceREST] {
			t.Errorf("REST not enabled on %q under wildcard", l.PathKey())
		}
	}
}

func TestBridge_Hide(t *testing.T) {
	b := New(newBridgeTree()).Expose("*", SurfaceREST).Hide("widget delete", SurfaceREST)
	for _, l := range b.Leaves() {
		got := l.Enabled[SurfaceREST]
		want := l.PathKey() != "widget delete"
		if got != want {
			t.Errorf("REST on %q: got=%v want=%v", l.PathKey(), got, want)
		}
	}
}

func TestBridge_Invoke_HappyPath(t *testing.T) {
	b := New(newBridgeTree())
	res, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"ping"},
		Meta: Meta{Surface: SurfaceLib},
	})
	if err != nil {
		t.Fatalf("Invoke err: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d want=0", res.ExitCode)
	}
	if res.Stdout == "" {
		t.Errorf("expected stdout output, got empty")
	}
}

func TestBridge_Invoke_DefaultsToSurfaceLib(t *testing.T) {
	b := New(newBridgeTree())
	// Meta.Surface omitted: bridge should default to SurfaceLib.
	if _, err := b.Invoke(context.Background(), Invocation{Path: []string{"ping"}}); err != nil {
		t.Fatalf("Invoke with empty Meta.Surface: %v", err)
	}
}

func TestBridge_Invoke_UnknownCommand(t *testing.T) {
	b := New(newBridgeTree())
	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"missing"},
		Meta: Meta{Surface: SurfaceLib},
	})
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("err=%v want ErrUnknownCommand", err)
	}
}

func TestBridge_Invoke_SurfaceNotEnabled(t *testing.T) {
	b := New(newBridgeTree())
	// REST is not in DefaultPolicy().DefaultEnabled, and we did not Expose it.
	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"ping"},
		Meta: Meta{Surface: SurfaceREST},
	})
	if !errors.Is(err, ErrSurfaceNotEnabled) {
		t.Fatalf("err=%v want ErrSurfaceNotEnabled", err)
	}
}

func TestBridge_Invoke_DestructiveBlocked(t *testing.T) {
	// Expose REST on a destructive leaf without AllowDestructiveOn:
	// surface enablement passes, but the policy gate refuses.
	b := New(newBridgeTree()).Expose("widget delete", SurfaceREST)
	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "delete"},
		Meta: Meta{Surface: SurfaceREST},
	})
	if !errors.Is(err, ErrDestructiveBlocked) {
		t.Fatalf("err=%v want ErrDestructiveBlocked", err)
	}
}

func TestBridge_Invoke_DestructiveOptIn(t *testing.T) {
	b := New(newBridgeTree(),
		WithPolicy(Policy{
			AllowDestructiveOn: []Surface{SurfaceREST},
			DefaultEnabled:     []Surface{SurfaceCLI, SurfaceLib},
		}),
	).Expose("widget delete", SurfaceREST)
	res, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "delete"},
		Meta: Meta{Surface: SurfaceREST},
	})
	if err != nil {
		t.Fatalf("Invoke err: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d want=0", res.ExitCode)
	}
}

func TestBridge_Invoke_WithCustomRunner(t *testing.T) {
	captured := make(chan Invocation, 1)
	runner := &fakeRunner{
		run: func(_ context.Context, inv Invocation) (Result, error) {
			captured <- inv
			return Result{Stdout: "fake"}, nil
		},
	}
	b := New(newBridgeTree(), WithRunner(runner))
	res, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"ping"},
		Meta: Meta{Surface: SurfaceLib},
	})
	if err != nil {
		t.Fatalf("Invoke err: %v", err)
	}
	if res.Stdout != "fake" {
		t.Errorf("Stdout=%q want=fake", res.Stdout)
	}
	got := <-captured
	if got.Path[0] != "ping" {
		t.Errorf("runner saw path=%v want=[ping]", got.Path)
	}
}

// fakeRunner is a test-only Runner whose Run callback drives
// assertions.
type fakeRunner struct {
	run func(context.Context, Invocation) (Result, error)
}

func (f *fakeRunner) Run(ctx context.Context, inv Invocation) (Result, error) {
	return f.run(ctx, inv)
}

func (f *fakeRunner) Stream(context.Context, Invocation, chan<- Event) error {
	return errors.New("fake stream")
}

func leafKeys(ls []*Leaf) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.PathKey())
	}
	return out
}

func sameSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := append([]string(nil), a...)
	sb := append([]string(nil), b...)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}
