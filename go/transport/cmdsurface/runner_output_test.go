package cmdsurface

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// testOutputCap is the cap the output-bound tests configure: small, so
// a test proves the bound without producing the default's 16MiB.
const testOutputCap = 64 << 10

// assertCapped checks that got is the first cap bytes of a stream of
// total bytes of fill, followed by the truncation marker.
func assertCapped(t *testing.T, stream, got string, fill byte, limit, total int) {
	t.Helper()
	marker := truncationMarker(strings.Repeat(string(fill), limit), limit, int64(total-limit))
	if len(got) > limit+len(marker) {
		t.Fatalf("%s: len=%d exceeds cap %d + marker %d", stream, len(got), limit, len(marker))
	}
	want := strings.Repeat(string(fill), limit) + marker
	if got != want {
		head := got
		if len(head) > 80 {
			head = "..." + head[len(head)-80:]
		}
		t.Fatalf("%s: len=%d tail=%q; want %d bytes of %q then %q", stream, len(got), head, limit, fill, marker)
	}
}

func TestSubprocessRunner_Run_CapsEachStream(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	const total = 1 << 20
	script := fmt.Sprintf(
		"head -c %[1]d /dev/zero | tr '\\0' o; head -c %[1]d /dev/zero | tr '\\0' e >&2; exit 3", total)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := SubprocessRunner(sh, WithOutputCap(testOutputCap)).Run(ctx, Invocation{
		Path: []string{"-c", script},
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	// The script reached its own exit: the child ran to completion
	// past the cap rather than being killed or blocked.
	if res.ExitCode != 3 {
		t.Fatalf("ExitCode=%d want=3", res.ExitCode)
	}
	if !res.Truncated {
		t.Error("Truncated=false want=true")
	}
	assertCapped(t, "stdout", res.Stdout, 'o', testOutputCap, total)
	assertCapped(t, "stderr", res.Stderr, 'e', testOutputCap, total)
}

func TestSubprocessRunner_Run_UnderCapUnchanged(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Exactly the cap is not over it.
	script := fmt.Sprintf("head -c %d /dev/zero | tr '\\0' o; printf warn >&2", testOutputCap)
	res, err := SubprocessRunner(sh, WithOutputCap(testOutputCap)).Run(ctx, Invocation{
		Path: []string{"-c", script},
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.Truncated {
		t.Error("Truncated=true want=false")
	}
	if res.Stdout != strings.Repeat("o", testOutputCap) {
		t.Errorf("Stdout len=%d want exactly %d bytes, no marker", len(res.Stdout), testOutputCap)
	}
	if res.Stderr != "warn" {
		t.Errorf("Stderr=%q want=warn", res.Stderr)
	}
}

func TestSubprocessRunner_Stream_DeliversAllPastCap(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	const n = 50000 // seq 1 50000 is ~290KB, well past the cap
	var want strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&want, "%d\n", i)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ch := make(chan Event, 64)
	errc := make(chan error, 1)
	go func() {
		errc <- SubprocessRunner(sh, WithOutputCap(testOutputCap)).Stream(ctx, Invocation{
			Path: []string{"-c", fmt.Sprintf("seq 1 %d", n)},
		}, ch)
	}()

	var got strings.Builder
	var done *Result
	for ev := range ch {
		switch ev.Kind {
		case "stdout":
			got.WriteString(ev.Data.(string))
			got.WriteByte('\n')
		case "done":
			done, _ = ev.Data.(*Result)
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	if got.String() != want.String() {
		t.Fatalf("events carried %d bytes, want all %d", got.Len(), want.Len())
	}
	if done == nil {
		t.Fatal("no done Result")
	}
	if !done.Truncated {
		t.Error("done Result Truncated=false want=true")
	}
	kept := want.String()[:testOutputCap]
	wantDone := kept + truncationMarker(kept, testOutputCap, int64(want.Len()-testOutputCap))
	if done.Stdout != wantDone {
		t.Errorf("done Stdout len=%d want %d (capped copy + marker)", len(done.Stdout), len(wantDone))
	}
}

// newBigOutputTree builds a root with one leaf, "big", that writes
// total bytes to stdout and to stderr and then fails.
func newBigOutputTree(total int) *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use: "big",
		RunE: func(cmd *cobra.Command, _ []string) error {
			chunk := []byte(strings.Repeat("o", 4096))
			echunk := []byte(strings.Repeat("e", 4096))
			for i := 0; i < total/len(chunk); i++ {
				_, _ = cmd.OutOrStdout().Write(chunk)
				_, _ = cmd.ErrOrStderr().Write(echunk)
			}
			return fmt.Errorf("done writing")
		},
	})
	return root
}

func TestInProcessRunner_Run_CapsEachStream(t *testing.T) {
	const total = 256 << 10
	res, err := InProcessRunner(newBigOutputTree(total), WithOutputCap(testOutputCap)).
		Run(context.Background(), Invocation{Path: []string{"big"}})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 1 {
		t.Errorf("ExitCode=%d want=1", res.ExitCode)
	}
	if !res.Truncated {
		t.Error("Truncated=false want=true")
	}
	assertCapped(t, "stdout", res.Stdout, 'o', testOutputCap, total)
	assertCapped(t, "stderr", res.Stderr, 'e', testOutputCap, total)
}

func TestInProcessRunner_Stream_DeliversAllPastCap(t *testing.T) {
	const n = 20000
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use: "seq",
		RunE: func(cmd *cobra.Command, _ []string) error {
			for i := 1; i <= n; i++ {
				fmt.Fprintf(cmd.OutOrStdout(), "%d\n", i)
			}
			return nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ch := make(chan Event, 64)
	errc := make(chan error, 1)
	go func() {
		errc <- InProcessRunner(root, WithOutputCap(4096)).Stream(ctx, Invocation{Path: []string{"seq"}}, ch)
	}()

	lines := 0
	var done *Result
	for ev := range ch {
		switch ev.Kind {
		case "stdout":
			lines++
			if want := fmt.Sprint(lines); ev.Data != want {
				t.Fatalf("line %d = %v", lines, ev.Data)
			}
		case "done":
			done, _ = ev.Data.(*Result)
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	if lines != n {
		t.Fatalf("stdout events=%d want=%d", lines, n)
	}
	if done == nil || !done.Truncated || len(done.Stdout) > 4096+128 {
		t.Fatalf("done Result not capped: %+v", done)
	}
}

func TestWithOutputCap_Default(t *testing.T) {
	var c runnerConfig
	if got := c.newCapture().limit; got != DefaultOutputCap {
		t.Errorf("default cap=%d want=%d", got, DefaultOutputCap)
	}
	if DefaultOutputCap != 16<<20 {
		t.Errorf("DefaultOutputCap=%d want 16MiB", DefaultOutputCap)
	}
	WithOutputCap(0)(&c)
	WithOutputCap(-1)(&c)
	if got := c.newCapture().limit; got != DefaultOutputCap {
		t.Errorf("non-positive cap changed the bound to %d", got)
	}
	WithOutputCap(10)(&c)
	if got := c.newCapture().limit; got != 10 {
		t.Errorf("cap=%d want=10", got)
	}
}

func TestCapturedOutput_Marker(t *testing.T) {
	c := (&runnerConfig{outputCap: 5}).newCapture()
	n, err := c.Write([]byte("abc"))
	if n != 3 || err != nil {
		t.Fatalf("Write=%d,%v", n, err)
	}
	if n, _ := c.Write([]byte("defgh")); n != 5 {
		t.Fatalf("Write past cap reported %d want 5", n)
	}
	if n, _ := c.Write([]byte("ij")); n != 2 {
		t.Fatalf("Write at cap reported %d want 2", n)
	}
	want := "abcde\n[cmdsurface: output truncated at 5 bytes; 5 bytes dropped]\n"
	if got := c.String(); got != want {
		t.Errorf("String=%q want=%q", got, want)
	}

	nl := (&runnerConfig{outputCap: 3}).newCapture()
	_, _ = nl.Write([]byte("ab\ncd"))
	if got, want := nl.String(), "ab\n[cmdsurface: output truncated at 3 bytes; 2 bytes dropped]\n"; got != want {
		t.Errorf("String=%q want=%q", got, want)
	}
}

// TestTruncated_SurvivesWireAndReplay pins that the flag reaches the
// RPC wire and survives an idempotency record, so a replayed answer
// says what the original did.
func TestTruncated_SurvivesWireAndReplay(t *testing.T) {
	res := Result{ExitCode: 3, Stdout: "x\n[marker]\n", Truncated: true}

	pb, err := resultToProto(res)
	if err != nil {
		t.Fatalf("resultToProto: %v", err)
	}
	if !pb.GetTruncated() {
		t.Error("proto Result truncated=false want=true")
	}

	raw, err := encodeIdemRecord("fp", res)
	if err != nil {
		t.Fatalf("encodeIdemRecord: %v", err)
	}
	got, _, err := decodeIdemRecord(raw)
	if err != nil {
		t.Fatalf("decodeIdemRecord: %v", err)
	}
	if !got.Truncated {
		t.Error("replayed Result Truncated=false want=true")
	}
}
