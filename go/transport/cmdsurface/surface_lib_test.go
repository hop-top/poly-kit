package cmdsurface_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/cmdsurface"
)

// libFakeRunner is the Library-surface test runner. It records each
// Invocation it sees and serves whatever Result the test wired in via
// RunFn; if RunFn is nil it returns a Result echoing the joined path
// on stdout. StreamFn drives Stream; when nil, two synthetic stdout
// events plus a done event are emitted.
type libFakeRunner struct {
	mu  sync.Mutex
	got []cmdsurface.Invocation

	RunFn    func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
	StreamFn func(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error
}

func (f *libFakeRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	f.record(inv)
	if f.RunFn != nil {
		return f.RunFn(ctx, inv)
	}
	return cmdsurface.Result{Stdout: strings.Join(inv.Path, " ")}, nil
}

func (f *libFakeRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	f.record(inv)
	if f.StreamFn != nil {
		return f.StreamFn(ctx, inv, out)
	}
	defer close(out)
	out <- cmdsurface.Event{Kind: "stdout", Data: "line-1", At: time.Now()}
	out <- cmdsurface.Event{Kind: "stdout", Data: "line-2", At: time.Now()}
	out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{Stdout: "line-1\nline-2"}, At: time.Now()}
	return nil
}

func (f *libFakeRunner) record(inv cmdsurface.Invocation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, inv)
}

func (f *libFakeRunner) captured() []cmdsurface.Invocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]cmdsurface.Invocation, len(f.got))
	copy(out, f.got)
	return out
}

// libTestTree builds the cobra tree used across the Library-surface
// tests:
//
//	root
//	├── widget
//	│   ├── add        (write)
//	│   ├── get        (read)
//	│   └── delete     (destructive)
//	└── ping           (read)
func libTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	widget := &cobra.Command{Use: "widget"}
	add := &cobra.Command{
		Use:         "add",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	get := &cobra.Command{
		Use:         "get",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	del := &cobra.Command{
		Use:         "delete",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	widget.AddCommand(add, get, del)
	root.AddCommand(widget)

	ping := &cobra.Command{
		Use:         "ping",
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Println("pong"); return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)
	return root
}

// libNewBridge constructs a bridge using libTestTree wired to a fresh
// libFakeRunner; returns both so tests can drive the runner.
func libNewBridge(t *testing.T, opts ...cmdsurface.Option) (*cmdsurface.Bridge, *libFakeRunner) {
	t.Helper()
	runner := &libFakeRunner{}
	allOpts := append([]cmdsurface.Option{cmdsurface.WithRunner(runner)}, opts...)
	b := cmdsurface.New(libTestTree(), allOpts...)
	return b, runner
}

func TestLibInvokeArgs_HappyPath(t *testing.T) {
	b, runner := libNewBridge(t)
	res, err := cmdsurface.InvokeArgs(context.Background(), b,
		[]string{"widget", "add", "--name", "foo"})
	if err != nil {
		t.Fatalf("InvokeArgs err: %v", err)
	}
	if res.Stdout != "widget add" {
		t.Errorf("stdout=%q want=%q", res.Stdout, "widget add")
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("runner captured %d invocations, want 1", len(got))
	}
	inv := got[0]
	if !libEqStrings(inv.Path, []string{"widget", "add"}) {
		t.Errorf("Path=%v want=[widget add]", inv.Path)
	}
	if inv.Flags["name"] != "foo" {
		t.Errorf("Flags[name]=%v want=foo", inv.Flags["name"])
	}
	if len(inv.Args) != 0 {
		t.Errorf("Args=%v want empty", inv.Args)
	}
}

func TestLibInvokeArgs_PathOnly(t *testing.T) {
	b, runner := libNewBridge(t)
	if _, err := cmdsurface.InvokeArgs(context.Background(), b, []string{"ping"}); err != nil {
		t.Fatalf("InvokeArgs err: %v", err)
	}
	inv := runner.captured()[0]
	if !libEqStrings(inv.Path, []string{"ping"}) {
		t.Errorf("Path=%v want=[ping]", inv.Path)
	}
	if len(inv.Args) != 0 {
		t.Errorf("Args=%v want empty", inv.Args)
	}
	if len(inv.Flags) != 0 {
		t.Errorf("Flags=%v want empty", inv.Flags)
	}
}

func TestLibInvokeArgs_EqualsFlagForm(t *testing.T) {
	b, runner := libNewBridge(t)
	if _, err := cmdsurface.InvokeArgs(context.Background(), b,
		[]string{"widget", "add", "--name=foo"}); err != nil {
		t.Fatalf("InvokeArgs err: %v", err)
	}
	inv := runner.captured()[0]
	if inv.Flags["name"] != "foo" {
		t.Errorf("Flags[name]=%v want=foo", inv.Flags["name"])
	}
}

func TestLibInvokeArgs_BareBoolFlag(t *testing.T) {
	b, runner := libNewBridge(t)
	if _, err := cmdsurface.InvokeArgs(context.Background(), b,
		[]string{"widget", "add", "--verbose"}); err != nil {
		t.Fatalf("InvokeArgs err: %v", err)
	}
	inv := runner.captured()[0]
	if inv.Flags["verbose"] != true {
		t.Errorf("Flags[verbose]=%v want=true", inv.Flags["verbose"])
	}
}

func TestLibInvokeArgs_PositionalAfterPath(t *testing.T) {
	b, runner := libNewBridge(t)
	if _, err := cmdsurface.InvokeArgs(context.Background(), b,
		[]string{"widget", "get", "42"}); err != nil {
		t.Fatalf("InvokeArgs err: %v", err)
	}
	inv := runner.captured()[0]
	if !libEqStrings(inv.Path, []string{"widget", "get"}) {
		t.Errorf("Path=%v want=[widget get]", inv.Path)
	}
	if !libEqStrings(inv.Args, []string{"42"}) {
		t.Errorf("Args=%v want=[42]", inv.Args)
	}
}

func TestLibInvokeArgs_UnknownPath(t *testing.T) {
	b, _ := libNewBridge(t)
	_, err := cmdsurface.InvokeArgs(context.Background(), b, []string{"bogus"})
	if !errors.Is(err, cmdsurface.ErrUnknownCommand) {
		t.Fatalf("err=%v want ErrUnknownCommand", err)
	}
}

func TestLibInvokeArgs_SurfaceNotEnabled(t *testing.T) {
	// Hide ping from SurfaceLib so the lib surface refuses it.
	b, _ := libNewBridge(t)
	b.Hide("ping", cmdsurface.SurfaceLib)
	_, err := cmdsurface.InvokeArgs(context.Background(), b, []string{"ping"})
	if !errors.Is(err, cmdsurface.ErrSurfaceNotEnabled) {
		t.Fatalf("err=%v want ErrSurfaceNotEnabled", err)
	}
}

// TestLibInvokeArgs_DestructiveAllowedByDefault asserts the
// permissive ceiling for SurfaceLib documented in safety.go:
// Policy.Allowed returns true for SurfaceLib regardless of
// AllowDestructiveOn. Adopters that need to refuse destructive
// commands over the lib surface have to write a custom Policy.
func TestLibInvokeArgs_DestructiveAllowedByDefault(t *testing.T) {
	b, runner := libNewBridge(t)
	res, err := cmdsurface.InvokeArgs(context.Background(), b,
		[]string{"widget", "delete"})
	if err != nil {
		t.Fatalf("InvokeArgs err: %v", err)
	}
	if res.Stdout != "widget delete" {
		t.Errorf("stdout=%q want=%q", res.Stdout, "widget delete")
	}
	inv := runner.captured()[0]
	if !libEqStrings(inv.Path, []string{"widget", "delete"}) {
		t.Errorf("Path=%v want=[widget delete]", inv.Path)
	}
}

func TestLibInvokeArgs_WithFlagOverridesArgv(t *testing.T) {
	b, runner := libNewBridge(t)
	if _, err := cmdsurface.InvokeArgs(context.Background(), b,
		[]string{"widget", "add", "--name", "a"},
		cmdsurface.WithFlag("name", "b"),
	); err != nil {
		t.Fatalf("InvokeArgs err: %v", err)
	}
	inv := runner.captured()[0]
	if inv.Flags["name"] != "b" {
		t.Errorf("Flags[name]=%v want=b (programmatic override)", inv.Flags["name"])
	}
}

func TestLibInvokeArgs_MetaFields(t *testing.T) {
	b, runner := libNewBridge(t)
	if _, err := cmdsurface.InvokeArgs(context.Background(), b,
		[]string{"ping"},
		cmdsurface.WithCaller("alice"),
		cmdsurface.WithTraceID("trace-42"),
		cmdsurface.WithExtra("source", "unit-test"),
	); err != nil {
		t.Fatalf("InvokeArgs err: %v", err)
	}
	inv := runner.captured()[0]
	if inv.Meta.Caller != "alice" {
		t.Errorf("Meta.Caller=%q want=alice", inv.Meta.Caller)
	}
	if inv.Meta.TraceID != "trace-42" {
		t.Errorf("Meta.TraceID=%q want=trace-42", inv.Meta.TraceID)
	}
	if inv.Meta.Extra["source"] != "unit-test" {
		t.Errorf("Meta.Extra[source]=%q want=unit-test", inv.Meta.Extra["source"])
	}
}

func TestLibInvokeArgs_MetaSurfaceForced(t *testing.T) {
	b, runner := libNewBridge(t)
	if _, err := cmdsurface.InvokeArgs(context.Background(), b, []string{"ping"}); err != nil {
		t.Fatalf("InvokeArgs err: %v", err)
	}
	if inv := runner.captured()[0]; inv.Meta.Surface != cmdsurface.SurfaceLib {
		t.Errorf("Meta.Surface=%q want=%q", inv.Meta.Surface, cmdsurface.SurfaceLib)
	}
}

func TestLibStreamArgs_HappyPath(t *testing.T) {
	b, runner := libNewBridge(t)
	out := make(chan cmdsurface.Event, 8)
	if err := cmdsurface.StreamArgs(context.Background(), b, []string{"ping"}, out); err != nil {
		t.Fatalf("StreamArgs err: %v", err)
	}
	var got []cmdsurface.Event
	for ev := range out {
		got = append(got, ev)
	}
	if len(got) != 3 {
		t.Fatalf("events=%d want 3 (stdout, stdout, done)", len(got))
	}
	if got[0].Kind != "stdout" || got[1].Kind != "stdout" || got[2].Kind != "done" {
		t.Errorf("event kinds=[%s %s %s] want=[stdout stdout done]",
			got[0].Kind, got[1].Kind, got[2].Kind)
	}
	// Confirm the streaming path also forces SurfaceLib on Meta.
	if inv := runner.captured()[0]; inv.Meta.Surface != cmdsurface.SurfaceLib {
		t.Errorf("Stream Meta.Surface=%q want=%q", inv.Meta.Surface, cmdsurface.SurfaceLib)
	}
}

// libEqStrings reports whether a and b have identical contents.
// nil and empty slices compare equal.
func libEqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
