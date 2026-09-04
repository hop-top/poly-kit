package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// newExecTree extends the runner fixture with the commands the
// execution-isolation tests need:
//
//	root
//	├── echo <text>      stdout = text, upper-cased under --loud
//	├── wait             blocks on ctx.Done, returns ctx.Err()
//	├── nap              sleeps napFor, prints "napped"
//	├── slurp            prints the number of bytes read from stdin
//	└── tags             --tag (StringSlice) printed joined by ","
func newExecTree() *cobra.Command {
	root := newFakeTree()

	wait := &cobra.Command{
		Use: "wait",
		RunE: func(cmd *cobra.Command, _ []string) error {
			<-cmd.Context().Done()
			return cmd.Context().Err()
		},
	}
	root.AddCommand(wait)

	nap := &cobra.Command{
		Use: "nap",
		RunE: func(cmd *cobra.Command, _ []string) error {
			select {
			case <-time.After(napFor):
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			}
			fmt.Fprint(cmd.OutOrStdout(), "napped")
			return nil
		},
	}
	root.AddCommand(nap)

	slurp := &cobra.Command{
		Use: "slurp",
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), strconv.Itoa(len(b)))
			return nil
		},
	}
	root.AddCommand(slurp)

	tags := &cobra.Command{
		Use: "tags",
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, _ := cmd.Flags().GetStringSlice("tag")
			fmt.Fprint(cmd.OutOrStdout(), strings.Join(got, ","))
			return nil
		},
	}
	tags.Flags().StringSlice("tag", []string{"base"}, "tags")
	root.AddCommand(tags)

	return root
}

const napFor = 60 * time.Millisecond

// TestInProcessRunner_FlagStateDoesNotLeak pins the rule that a
// flag parsed by one invocation does not persist into the next.
// pflag keeps Value and Changed on the FlagSet; without a reset the
// second call below sees --loud from the first and shouts.
func TestInProcessRunner_FlagStateDoesNotLeak(t *testing.T) {
	r := InProcessRunner(newExecTree())
	ctx := context.Background()

	first, err := r.Run(ctx, Invocation{
		Path: []string{"echo"}, Args: []string{"hi"},
		Flags: map[string]any{"loud": true},
	})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Stdout != "HI" {
		t.Fatalf("first Stdout=%q want=HI", first.Stdout)
	}

	second, err := r.Run(ctx, Invocation{Path: []string{"echo"}, Args: []string{"hi"}})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Stdout != "hi" {
		t.Errorf("second Stdout=%q want=hi: --loud leaked from the first invocation", second.Stdout)
	}
}

// TestInProcessRunner_SliceFlagResetsExactly covers the pflag slice
// quirk: after the first Set, later Sets append rather than replace.
// A reset that only restores the default would make the third call
// report "base,b" instead of "b".
func TestInProcessRunner_SliceFlagResetsExactly(t *testing.T) {
	r := InProcessRunner(newExecTree())
	ctx := context.Background()

	cases := []struct {
		flags map[string]any
		want  string
	}{
		{map[string]any{"tag": "a"}, "a"},
		{nil, "base"},
		{map[string]any{"tag": "b"}, "b"},
		{nil, "base"},
	}
	for i, tc := range cases {
		res, err := r.Run(ctx, Invocation{Path: []string{"tags"}, Flags: tc.flags})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if res.Stdout != tc.want {
			t.Errorf("call %d: Stdout=%q want=%q", i, res.Stdout, tc.want)
		}
	}
}

// TestInProcessRunner_ContextReachesCommand pins that the caller's
// context is the one the command observes, and that canceling it
// ends a blocked command and is reported as a cancellation rather
// than as a command failure.
func TestInProcessRunner_ContextReachesCommand(t *testing.T) {
	r := InProcessRunner(newExecTree())
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := r.Run(ctx, Invocation{Path: []string{"wait"}})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Run did not return promptly after cancel: %v", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err=%v want context.Canceled", err)
	}
}

// TestInProcessRunner_Stream_ContextReachesCommand is the streaming
// twin: the done event still arrives and Stream reports the
// cancellation.
func TestInProcessRunner_Stream_ContextReachesCommand(t *testing.T) {
	r := InProcessRunner(newExecTree())
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan Event, 16)
	errc := make(chan error, 1)
	go func() { errc <- r.Stream(ctx, Invocation{Path: []string{"wait"}}, ch) }()
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	var done bool
	for ev := range ch {
		if ev.Kind == "done" {
			done = true
		}
	}
	if !done {
		t.Error("no done event after cancellation")
	}
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Errorf("Stream err=%v want context.Canceled", err)
	}
}

// TestInProcessRunner_StaleContextIsNotReused pins the cobra quirk
// that a command keeps the context of the first execution unless it
// is set again. The leaf starts out holding an already-canceled
// context — the state a leaf is left in after any earlier execution
// or a hook's SetContext — and the runner's invocation must observe
// its own context, not that one.
func TestInProcessRunner_StaleContextIsNotReused(t *testing.T) {
	root := newExecTree()
	r := InProcessRunner(root)

	dead, cancelDead := context.WithCancel(context.Background())
	cancelDead()
	leaf, _, err := root.Find([]string{"wait"})
	if err != nil {
		t.Fatal(err)
	}
	leaf.SetContext(dead)

	live, cancelLive := context.WithCancel(context.Background())
	defer cancelLive()
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancelLive()
	}()
	start := time.Now()
	_, err = r.Run(live, Invocation{Path: []string{"wait"}})
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err=%v want context.Canceled", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("Run returned after %v: the command observed the stale context", elapsed)
	}

	// And the run itself leaves no context behind for the next one.
	if _, err := r.Run(dead, Invocation{Path: []string{"wait"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("dead Run err=%v want context.Canceled", err)
	}
	live2, cancel2 := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel2()
	start = time.Now()
	if _, err := r.Run(live2, Invocation{Path: []string{"wait"}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live Run err=%v want context.DeadlineExceeded", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Error("the second live Run observed the previous invocation's context")
	}
}

// TestInProcessRunner_TreeIsCleanBetweenInvocations pins that the
// tree carries nothing of an invocation once it returns: the flag it
// set is back at its baseline value with Changed cleared, and the
// leaf holds no context, so anything else reading the tree between
// invocations sees the state the runner was handed.
func TestInProcessRunner_TreeIsCleanBetweenInvocations(t *testing.T) {
	root := newExecTree()
	r := InProcessRunner(root)
	if _, err := r.Run(context.Background(), Invocation{
		Path: []string{"echo"}, Args: []string{"x"}, Flags: map[string]any{"loud": true},
	}); err != nil {
		t.Fatal(err)
	}
	leaf, _, err := root.Find([]string{"echo"})
	if err != nil {
		t.Fatal(err)
	}
	f := leaf.Flags().Lookup("loud")
	if f.Value.String() != "false" || f.Changed {
		t.Errorf("after the run --loud is %s (Changed=%v); want the baseline false/unchanged", f.Value, f.Changed)
	}
	if leaf.Context() != nil {
		t.Error("the leaf still holds the invocation's context")
	}
}

// TestInProcessRunner_NoStdin pins that a served invocation has no
// standard input: a command that reads stdin gets EOF at once rather
// than blocking on, or reading from, the serving process's terminal.
func TestInProcessRunner_NoStdin(t *testing.T) {
	root := newExecTree()
	root.SetIn(strings.NewReader("operator typed this"))
	r := InProcessRunner(root)

	res, err := r.Run(context.Background(), Invocation{Path: []string{"slurp"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "0" {
		t.Errorf("command read %s bytes from stdin; want 0", res.Stdout)
	}
}

// TestBridge_ConcurrentInvocationsAreIsolated drives N concurrent
// invocations through Bridge.Invoke with a mix of flag values and
// checks every result matches its own request. Run under -race.
func TestBridge_ConcurrentInvocationsAreIsolated(t *testing.T) {
	b := New(newExecTree())
	const n = 64

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			text := "w" + strconv.Itoa(i)
			loud := i%3 == 0
			inv := Invocation{
				Path: []string{"echo"}, Args: []string{text},
				Meta: Meta{Surface: SurfaceLib},
			}
			if loud {
				inv.Flags = map[string]any{"loud": true}
			}
			res, err := b.Invoke(context.Background(), inv)
			if err != nil {
				errs <- fmt.Errorf("invocation %d: %w", i, err)
				return
			}
			want := text
			if loud {
				want = strings.ToUpper(text)
			}
			if res.Stdout != want {
				errs <- fmt.Errorf("invocation %d: Stdout=%q want=%q", i, res.Stdout, want)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestInProcessRunner_SharedTreeSerializes documents the throughput
// consequence of a shared cobra tree: invocations run one at a time.
func TestInProcessRunner_SharedTreeSerializes(t *testing.T) {
	r := InProcessRunner(newExecTree())
	const n = 4
	elapsed := timeParallelNaps(t, r, n)
	if elapsed < n*napFor {
		t.Errorf("%d naps of %v finished in %v: a shared tree must serialize", n, napFor, elapsed)
	}
}

// TestInProcessRunner_RootFactoryRunsInParallel pins the opposite:
// a runner handed a root factory gives each invocation its own tree
// and runs them concurrently, with no shared cobra state to race on.
func TestInProcessRunner_RootFactoryRunsInParallel(t *testing.T) {
	r := InProcessRunner(nil, WithRootFactory(newExecTree))
	const n = 4
	elapsed := timeParallelNaps(t, r, n)
	if elapsed >= n*napFor {
		t.Errorf("%d naps of %v took %v: a factory runner must not serialize", n, napFor, elapsed)
	}

	// Per-invocation trees also carry no flag state across calls.
	first, err := r.Run(context.Background(), Invocation{
		Path: []string{"echo"}, Args: []string{"hi"}, Flags: map[string]any{"loud": true},
	})
	if err != nil || first.Stdout != "HI" {
		t.Fatalf("first Run: %q, %v", first.Stdout, err)
	}
	second, err := r.Run(context.Background(), Invocation{Path: []string{"echo"}, Args: []string{"hi"}})
	if err != nil || second.Stdout != "hi" {
		t.Fatalf("second Run: %q, %v", second.Stdout, err)
	}
}

func timeParallelNaps(t *testing.T, r Runner, n int) time.Duration {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := time.Now()
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := r.Run(context.Background(), Invocation{Path: []string{"nap"}})
			if err != nil {
				errs <- err
				return
			}
			if res.Stdout != "napped" {
				errs <- fmt.Errorf("Stdout=%q want=napped", res.Stdout)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	return time.Since(start)
}

// TestSubprocessRunner_CancelKillsChild pins that canceling the
// caller's context does not merely return: the child the runner
// spawned is gone. The shell prints the pid of a background sleep
// and waits on it; after the cancel that pid must not exist.
func TestSubprocessRunner_CancelKillsChild(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	for _, mode := range []string{"run", "stream"} {
		t.Run(mode, func(t *testing.T) {
			r := SubprocessRunner(sh)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			inv := Invocation{Args: []string{"-c", "sleep 30 & echo $!; wait"}}

			var stdout string
			switch mode {
			case "run":
				go func() {
					time.Sleep(150 * time.Millisecond)
					cancel()
				}()
				res, err := r.Run(ctx, inv)
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v want context.Canceled", err)
				}
				stdout = res.Stdout
			case "stream":
				ch := make(chan Event, 32)
				errc := make(chan error, 1)
				go func() { errc <- r.Stream(ctx, inv, ch) }()
				for ev := range ch {
					if ev.Kind == "stdout" {
						stdout += ev.Data.(string) + "\n"
						cancel() // the pid line is out: the child is up
					}
				}
				if err := <-errc; !errors.Is(err, context.Canceled) {
					t.Fatalf("Stream err=%v want context.Canceled", err)
				}
			}

			pid, err := strconv.Atoi(strings.TrimSpace(stdout))
			if err != nil {
				t.Fatalf("no pid on stdout: %q", stdout)
			}
			deadline := time.Now().Add(2 * time.Second)
			for processAlive(pid) {
				if time.Now().After(deadline) {
					t.Fatalf("child %d still alive after cancellation", pid)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
