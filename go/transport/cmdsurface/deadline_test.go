package cmdsurface

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/storage/kv/memory"
)

// deadlineTree is a root with commands that wait on their context:
//
//	root
//	├── wait      (waits up to --for, returns early on cancel)
//	├── bounded   (the same, annotated kit/timeout: 100ms)
//	├── patient   (the same, annotated kit/timeout: 2s)
//	└── stubborn  (sleeps --for, ignoring its context)
func deadlineTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	waiter := func(use string, ann map[string]string) *cobra.Command {
		c := &cobra.Command{
			Use:         use,
			Annotations: map[string]string{"kit/side-effect": "read"},
			RunE: func(cmd *cobra.Command, _ []string) error {
				d, _ := cmd.Flags().GetDuration("for")
				cmd.Println("started")
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(d):
					cmd.Println("finished")
					return nil
				}
			},
		}
		for k, v := range ann {
			c.Annotations[k] = v
		}
		c.Flags().Duration("for", time.Second, "how long to wait")
		return c
	}
	root.AddCommand(
		waiter("wait", nil),
		waiter("bounded", map[string]string{AnnotationTimeout: "100ms"}),
		waiter("patient", map[string]string{AnnotationTimeout: "2s"}),
	)
	stubborn := &cobra.Command{
		Use:         "stubborn",
		Annotations: map[string]string{"kit/side-effect": "read", AnnotationTimeout: "50ms"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, _ := cmd.Flags().GetDuration("for")
			time.Sleep(d)
			cmd.Println("finished")
			return nil
		},
	}
	stubborn.Flags().Duration("for", time.Second, "how long to sleep")
	root.AddCommand(stubborn)
	return root
}

func invokeFor(path string, d time.Duration) Invocation {
	return Invocation{
		Path:  []string{path},
		Flags: map[string]any{"for": d.String()},
		Meta:  Meta{Surface: SurfaceMCP},
	}
}

func TestDeadline_AnnotationCutsTheRunShort(t *testing.T) {
	log := &admissionLog{}
	b := New(deadlineTree(), WithSinks(SinkSpec{Sink: log, OnError: true, OnOK: true}))

	start := time.Now()
	res, err := b.Invoke(context.Background(), invokeFor("bounded", 5*time.Second))
	if !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want ErrDeadlineExceeded", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v does not wrap context.DeadlineExceeded", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the run took %s; the 100ms deadline did not cancel it", took)
	}
	if !strings.Contains(err.Error(), "bounded ran past its 100ms deadline") {
		t.Fatalf("err = %q, want it to name the command and the bound", err)
	}
	if !strings.Contains(res.Stdout, "started") {
		t.Fatalf("partial result lost: %+v", res)
	}
	if len(log.errs) != 1 || !errors.Is(log.errs[0], ErrDeadlineExceeded) {
		t.Fatalf("audit = %v, want one deadline_exceeded record", log.errs)
	}
}

func TestDeadline_BridgeDefaultAppliesWithoutAnnotation(t *testing.T) {
	b := New(deadlineTree(), WithCommandTimeout(100*time.Millisecond))
	_, err := b.Invoke(context.Background(), invokeFor("wait", 5*time.Second))
	if !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want ErrDeadlineExceeded from the default", err)
	}

	res, err := b.Invoke(context.Background(), invokeFor("wait", 10*time.Millisecond))
	if err != nil || !strings.Contains(res.Stdout, "finished") {
		t.Fatalf("a call inside the default = %+v, %v; want it to finish", res, err)
	}
}

func TestDeadline_NoneByDefault(t *testing.T) {
	b := New(deadlineTree())
	res, err := b.Invoke(context.Background(), invokeFor("wait", 300*time.Millisecond))
	if err != nil || !strings.Contains(res.Stdout, "finished") {
		t.Fatalf("Invoke = %+v, %v; with no deadline configured the call must finish", res, err)
	}
}

func TestDeadline_AnnotationWinsOverTheDefault(t *testing.T) {
	b := New(deadlineTree(), WithCommandTimeout(50*time.Millisecond))
	res, err := b.Invoke(context.Background(), invokeFor("patient", 300*time.Millisecond))
	if err != nil || !strings.Contains(res.Stdout, "finished") {
		t.Fatalf("Invoke = %+v, %v; kit/timeout 2s must outrank a 50ms default", res, err)
	}
}

func TestDeadline_CallerMayShortenNeverLengthen(t *testing.T) {
	b := New(deadlineTree())

	// Shorter than the annotation: the caller's wins.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := b.Invoke(ctx, invokeFor("patient", 5*time.Second))
	if !errors.Is(err, ErrDeadlineExceeded) || !strings.Contains(err.Error(), "the caller's deadline") {
		t.Fatalf("err = %v, want the caller's shorter deadline reported", err)
	}

	// Longer than the annotation: the annotation still holds.
	long, cancelLong := context.WithTimeout(context.Background(), time.Minute)
	defer cancelLong()
	start := time.Now()
	_, err = b.Invoke(long, invokeFor("bounded", 5*time.Second))
	if !errors.Is(err, ErrDeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %s; a longer caller deadline must not lift kit/timeout", err, time.Since(start))
	}
}

func TestDeadline_ACommandThatCompletesDespiteItCompleted(t *testing.T) {
	b := New(deadlineTree())
	res, err := b.Invoke(context.Background(), invokeFor("stubborn", 150*time.Millisecond))
	if err != nil || !strings.Contains(res.Stdout, "finished") {
		t.Fatalf("Invoke = %+v, %v; a command that ignored its context and finished is a completion", res, err)
	}
}

func TestDeadline_StreamDeliversDoneThenTheRefusal(t *testing.T) {
	b := New(deadlineTree())
	adm, err := b.Admit(context.Background(), invokeFor("bounded", 5*time.Second))
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	out := make(chan Event, 16)
	errc := make(chan error, 1)
	go func() { errc <- adm.Stream(context.Background(), out) }()
	var kinds []string
	for ev := range out {
		kinds = append(kinds, ev.Kind)
	}
	if err := <-errc; !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("Stream err = %v, want ErrDeadlineExceeded", err)
	}
	if len(kinds) == 0 || kinds[len(kinds)-1] != "done" {
		t.Fatalf("events = %v, want a terminal done", kinds)
	}
}

// TestDeadline_NotArmedUntilTheRun pins that the deadline starts at the
// capacity slot, when the admitted call runs: time spent between Admit
// and Run — a person answering a confirmation — does not count.
func TestDeadline_NotArmedUntilTheRun(t *testing.T) {
	b := New(deadlineTree())
	adm, err := b.Admit(context.Background(), invokeFor("bounded", 10*time.Millisecond))
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // longer than the 100ms bound
	res, err := adm.Run(context.Background())
	if err != nil || !strings.Contains(res.Stdout, "finished") {
		t.Fatalf("Run = %+v, %v; the wait before the run must not spend the deadline", res, err)
	}
}

// TestDeadline_KillsTheSubprocessGroup pins that the deadline reaches a
// subprocess runner: the child's process group is killed, a helper it
// spawned included.
func TestDeadline_KillsTheSubprocessGroup(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	sub := SubprocessRunner(sh)
	// The leaf is bridge-resolved; the runner runs a shell that spawns
	// a background sleep, prints its pid and waits on it.
	runner := &fakeRunner{run: func(ctx context.Context, _ Invocation) (Result, error) {
		return sub.Run(ctx, Invocation{Path: []string{"-c", "sleep 30 & echo $!; wait"}})
	}}
	b := New(deadlineTree(), WithRunner(runner), WithCommandTimeout(500*time.Millisecond))

	start := time.Now()
	res, err := b.Invoke(context.Background(), invokeFor("wait", 0))
	if !errors.Is(err, ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want ErrDeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the subprocess outlived its deadline by %s", time.Since(start))
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if perr != nil {
		t.Fatalf("no pid on stdout: %q", res.Stdout)
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d still alive after the deadline", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCommandTimeout_Annotation(t *testing.T) {
	cases := []struct {
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{"30s", 30 * time.Second, false},
		{" 2m ", 2 * time.Minute, false},
		{"0s", 0, true},
		{"-1s", 0, true},
		{"soon", 0, true},
		{"30", 0, true},
	}
	for _, tc := range cases {
		cmd := &cobra.Command{Use: "x", Annotations: map[string]string{AnnotationTimeout: tc.raw}}
		got, err := CommandTimeout(cmd)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("CommandTimeout(%q) = %s, %v; want %s, err=%v", tc.raw, got, err, tc.want, tc.wantErr)
		}
	}
	if d, err := CommandTimeout(&cobra.Command{Use: "x"}); d != 0 || err != nil {
		t.Errorf("no annotation = %s, %v; want 0, nil", d, err)
	}
}

func TestValidateCommandTimeouts_NamesEveryBadCommand(t *testing.T) {
	root := deadlineTree()
	root.AddCommand(&cobra.Command{Use: "bad", Annotations: map[string]string{AnnotationTimeout: "soon"}})
	group := &cobra.Command{Use: "group"}
	group.AddCommand(&cobra.Command{Use: "worse", Annotations: map[string]string{AnnotationTimeout: "-5s"}})
	root.AddCommand(group)

	err := ValidateCommandTimeouts(root)
	if err == nil {
		t.Fatal("malformed kit/timeout annotations validated")
	}
	for _, want := range []string{"root bad", "root group worse"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}
	if err := ValidateCommandTimeouts(deadlineTree()); err != nil {
		t.Errorf("a well-formed tree = %v", err)
	}
}

// A read the result cache handles runs under the same deadline: a
// miss that runs past it is cut short as ErrDeadlineExceeded, and,
// being an error, stores nothing.
func TestDeadline_BoundsResultCacheMiss(t *testing.T) {
	var calls atomic.Int64
	run := &ctxRunner{run: func(ctx context.Context) (Result, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return Result{}, ctx.Err()
		}
		return Result{Data: map[string]any{"ok": true}}, nil
	}}
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	b := New(newCacheTree(), WithRunner(run), WithResultCache(store),
		WithCommandTimeout(20*time.Millisecond))
	b.Expose("*", SurfaceREST)

	adm, err := b.Admit(context.Background(), cacheRESTCall("widget", "list"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := adm.Run(context.Background()); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrDeadlineExceeded) {
			t.Fatalf("Run = %v, want ErrDeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cache miss ran past its deadline")
	}

	res, info, _ := call(t, b, cacheRESTCall("widget", "list"))
	if info.Hit || res.Data == nil {
		t.Fatalf("second call: Hit=%v Data=%v, want a fresh run", info.Hit, res.Data)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("runner ran %d times, want 2", n)
	}
}

// ctxRunner runs every invocation through run, which sees the run's
// context.
type ctxRunner struct {
	run func(ctx context.Context) (Result, error)
}

func (r *ctxRunner) Run(ctx context.Context, _ Invocation) (Result, error) { return r.run(ctx) }

func (r *ctxRunner) Stream(ctx context.Context, _ Invocation, out chan<- Event) error {
	defer close(out)
	res, err := r.run(ctx)
	out <- Event{Kind: "done", Data: &res}
	return err
}
