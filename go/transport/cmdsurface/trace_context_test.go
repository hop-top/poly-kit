package cmdsurface

import (
	"context"
	"strings"
	"testing"
)

const testTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// printTraceEnv prints the child's trace variables, "-" when unset.
const printTraceEnv = `printf '%s|%s' "${TRACEPARENT:--}" "${TRACESTATE:--}"`

func TestSubprocessRunner_PassesTraceContextToChild(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	// The server's own inherited trace context must be replaced, not
	// duplicated beside the invocation's.
	t.Setenv("TRACEPARENT", "00-11111111111111111111111111111111-2222222222222222-01")
	t.Setenv("TRACESTATE", "stale=1")

	inv := Invocation{
		Args:    []string{"-c", printTraceEnv},
		ownArgv: true, // sh parses its own argv: no "--" before -c
		Meta:    Meta{Traceparent: testTraceparent, Tracestate: "vendor=x"},
	}
	want := testTraceparent + "|vendor=x"

	res, err := SubprocessRunner(sh).Run(context.Background(), inv)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("Run: %v exit=%d stderr=%q", err, res.ExitCode, res.Stderr)
	}
	if res.Stdout != want {
		t.Errorf("Run child env = %q, want %q", res.Stdout, want)
	}

	out := make(chan Event, 8)
	if err := SubprocessRunner(sh).Stream(context.Background(), inv, out); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var lines []string
	for ev := range out {
		if ev.Kind == "stdout" {
			lines = append(lines, ev.Data.(string))
		}
	}
	if got := strings.Join(lines, ""); got != want {
		t.Errorf("Stream child env = %q, want %q", got, want)
	}
}

func TestSubprocessRunner_StaleTracestateIsNotInherited(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	// The server's tracestate belongs to the server's parent; beside
	// the invocation's traceparent it would describe another trace.
	t.Setenv("TRACESTATE", "stale=1")

	res, err := SubprocessRunner(sh).Run(context.Background(), Invocation{
		Args:    []string{"-c", printTraceEnv},
		ownArgv: true, // sh parses its own argv: no "--" before -c
		Meta:    Meta{Traceparent: testTraceparent},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := testTraceparent + "|-"; res.Stdout != want {
		t.Errorf("child env = %q, want %q", res.Stdout, want)
	}
}

func TestSubprocessRunner_NoTraceContextInheritsEnvironment(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	t.Setenv("TRACEPARENT", testTraceparent)
	t.Setenv("TRACESTATE", "")

	res, err := SubprocessRunner(sh).Run(context.Background(), Invocation{
		Args:    []string{"-c", printTraceEnv},
		ownArgv: true, // sh parses its own argv: no "--" before -c
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := testTraceparent + "|-"; res.Stdout != want {
		t.Errorf("child env = %q, want the server's own %q", res.Stdout, want)
	}
}

// tagRunner records the order middleware saw an invocation in.
type tagRunner struct {
	tag   string
	next  Runner
	trail *[]string
}

func (r tagRunner) Run(ctx context.Context, inv Invocation) (Result, error) {
	*r.trail = append(*r.trail, r.tag)
	return r.next.Run(ctx, inv)
}

func (r tagRunner) Stream(ctx context.Context, inv Invocation, out chan<- Event) error {
	*r.trail = append(*r.trail, r.tag)
	return r.next.Stream(ctx, inv, out)
}

func TestWithRunnerMiddleware_WrapsWhicheverRunnerIsInForce(t *testing.T) {
	var trail []string
	mw := func(tag string) func(Runner) Runner {
		return func(next Runner) Runner { return tagRunner{tag: tag, next: next, trail: &trail} }
	}
	inner := &fakeRunner{run: func(context.Context, Invocation) (Result, error) {
		trail = append(trail, "runner")
		return Result{}, nil
	}}

	// The middleware option comes before WithRunner and still wraps it.
	b := New(newFakeTree(), WithRunnerMiddleware(mw("outer")), WithRunner(inner), WithRunnerMiddleware(mw("inner"), nil))
	b.Expose("*", SurfaceREST)

	if _, err := b.Invoke(context.Background(), Invocation{Path: []string{"echo"}, Meta: Meta{Surface: SurfaceREST}}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got := strings.Join(trail, ">"); got != "outer>inner>runner" {
		t.Errorf("order = %s, want outer>inner>runner", got)
	}

	// Streaming surfaces reach the runner through Bridge.Runner.
	trail = nil
	out := make(chan Event, 1)
	_ = b.Runner().Stream(context.Background(), Invocation{}, out)
	if got := strings.Join(trail, ">"); got != "outer>inner" {
		t.Errorf("stream order = %s, want outer>inner", got)
	}

	// A refusal never reaches the middleware.
	trail = nil
	_, _ = b.Invoke(context.Background(), Invocation{Path: []string{"nope"}, Meta: Meta{Surface: SurfaceREST}})
	if len(trail) != 0 {
		t.Errorf("a refused invocation reached %v", trail)
	}
}
