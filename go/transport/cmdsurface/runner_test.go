package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// newFakeTree builds a tiny cobra tree used by the runner tests:
//
//	root
//	├── echo <text>            stdout = text
//	├── shout <text>           stdout + stderr = TEXT
//	├── boom                   returns error "kaboom"
//	└── lines                  stdout: "a\nb\nc"; stderr: "warn1\nwarn2"
func newFakeTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	echo := &cobra.Command{
		Use: "echo [text]",
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args, " ")
			if loud, _ := cmd.Flags().GetBool("loud"); loud {
				text = strings.ToUpper(text)
			}
			fmt.Fprint(cmd.OutOrStdout(), text)
			return nil
		},
	}
	echo.Flags().Bool("loud", false, "upper-case the text")
	root.AddCommand(echo)

	shout := &cobra.Command{
		Use: "shout [text]",
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.ToUpper(strings.Join(args, " "))
			fmt.Fprint(cmd.OutOrStdout(), text)
			fmt.Fprint(cmd.ErrOrStderr(), text)
			return nil
		},
	}
	root.AddCommand(shout)

	boom := &cobra.Command{
		Use: "boom",
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("kaboom")
		},
	}
	root.AddCommand(boom)

	lines := &cobra.Command{
		Use: "lines",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "a")
			fmt.Fprintln(cmd.OutOrStdout(), "b")
			fmt.Fprintln(cmd.OutOrStdout(), "c")
			fmt.Fprintln(cmd.ErrOrStderr(), "warn1")
			fmt.Fprintln(cmd.ErrOrStderr(), "warn2")
			return nil
		},
	}
	root.AddCommand(lines)

	return root
}

func TestInProcessRunner_Run_HappyPath(t *testing.T) {
	r := InProcessRunner(newFakeTree())
	res, err := r.Run(context.Background(), Invocation{
		Path: []string{"echo"},
		Args: []string{"hello", "world"},
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d want=0", res.ExitCode)
	}
	if res.Stdout != "hello world" {
		t.Errorf("Stdout=%q want=%q", res.Stdout, "hello world")
	}
	if res.Stderr != "" {
		t.Errorf("Stderr=%q want=empty", res.Stderr)
	}
}

func TestInProcessRunner_Run_Flag(t *testing.T) {
	r := InProcessRunner(newFakeTree())
	res, err := r.Run(context.Background(), Invocation{
		Path:  []string{"echo"},
		Args:  []string{"hi"},
		Flags: map[string]any{"loud": true},
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.Stdout != "HI" {
		t.Errorf("Stdout=%q want=%q", res.Stdout, "HI")
	}
}

func TestInProcessRunner_Run_StdoutAndStderr(t *testing.T) {
	r := InProcessRunner(newFakeTree())
	res, err := r.Run(context.Background(), Invocation{
		Path: []string{"shout"},
		Args: []string{"ok"},
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.Stdout != "OK" {
		t.Errorf("Stdout=%q want=OK", res.Stdout)
	}
	if res.Stderr != "OK" {
		t.Errorf("Stderr=%q want=OK", res.Stderr)
	}
}

func TestInProcessRunner_Run_Error(t *testing.T) {
	r := InProcessRunner(newFakeTree())
	res, err := r.Run(context.Background(), Invocation{Path: []string{"boom"}})
	if err != nil {
		t.Fatalf("Run err: %v (Runner.Run should not bubble execErr)", err)
	}
	if res.ExitCode != 1 {
		t.Errorf("ExitCode=%d want=1", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "kaboom") {
		t.Errorf("Stderr=%q want to contain 'kaboom'", res.Stderr)
	}
}

func TestInProcessRunner_Run_UnknownPath(t *testing.T) {
	r := InProcessRunner(newFakeTree())
	_, err := r.Run(context.Background(), Invocation{Path: []string{"nope"}})
	if err == nil {
		t.Fatal("Run expected error for unknown path")
	}
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("expected ErrUnknownCommand, got %v", err)
	}
}

func TestInProcessRunner_Stream_Lines(t *testing.T) {
	r := InProcessRunner(newFakeTree())
	ch := make(chan Event, 16)
	errc := make(chan error, 1)
	go func() {
		errc <- r.Stream(context.Background(), Invocation{Path: []string{"lines"}}, ch)
	}()

	var stdoutLines, stderrLines []string
	var done *Event
collect:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break collect
			}
			switch ev.Kind {
			case "stdout":
				stdoutLines = append(stdoutLines, ev.Data.(string))
			case "stderr":
				stderrLines = append(stderrLines, ev.Data.(string))
			case "done":
				d := ev
				done = &d
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for stream events; got stdout=%v stderr=%v done=%v",
				stdoutLines, stderrLines, done)
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	wantOut := []string{"a", "b", "c"}
	if !equalStrings(stdoutLines, wantOut) {
		t.Errorf("stdout lines=%v want=%v", stdoutLines, wantOut)
	}
	wantErr := []string{"warn1", "warn2"}
	if !equalStrings(stderrLines, wantErr) {
		t.Errorf("stderr lines=%v want=%v", stderrLines, wantErr)
	}
	if done == nil {
		t.Fatal("expected terminal done Event")
	}
	res, ok := done.Data.(*Result)
	if !ok {
		t.Fatalf("done.Data type=%T want=*Result", done.Data)
	}
	if res.ExitCode != 0 {
		t.Errorf("done Result.ExitCode=%d want=0", res.ExitCode)
	}
}

func TestInProcessRunner_Stream_Error(t *testing.T) {
	r := InProcessRunner(newFakeTree())
	ch := make(chan Event, 8)
	errc := make(chan error, 1)
	go func() {
		errc <- r.Stream(context.Background(), Invocation{Path: []string{"boom"}}, ch)
	}()
	var done *Event
	for ev := range ch {
		if ev.Kind == "done" {
			d := ev
			done = &d
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	if done == nil {
		t.Fatal("expected terminal done event")
	}
	res, ok := done.Data.(*Result)
	if !ok {
		t.Fatalf("done.Data type=%T", done.Data)
	}
	if res.ExitCode != 1 {
		t.Errorf("done Result.ExitCode=%d want=1", res.ExitCode)
	}
}

// findSh locates /bin/sh (or /usr/bin/sh) for tests that need to
// execute a real binary. Returns "" if neither is present, in which
// case the calling test SHOULD t.Skip — these are integration-flavored
// tests that depend on a POSIX shell.
func findSh(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"/bin/sh", "/usr/bin/sh"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func TestSubprocessRunner_Run_Success(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	r := SubprocessRunner(sh)
	res, err := r.Run(context.Background(), Invocation{
		Args: []string{"-c", "printf hello"},
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d want=0", res.ExitCode)
	}
	if res.Stdout != "hello" {
		t.Errorf("Stdout=%q want=%q", res.Stdout, "hello")
	}
	if res.Stderr != "" {
		t.Errorf("Stderr=%q want=empty", res.Stderr)
	}
}

func TestSubprocessRunner_Run_NonZeroExit(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	r := SubprocessRunner(sh)
	res, err := r.Run(context.Background(), Invocation{
		Args: []string{"-c", "exit 7"},
	})
	if err != nil {
		t.Fatalf("Run err: %v (ExitError should not bubble)", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("ExitCode=%d want=7", res.ExitCode)
	}
}

func TestSubprocessRunner_Run_StderrCapture(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	r := SubprocessRunner(sh)
	res, err := r.Run(context.Background(), Invocation{
		Args: []string{"-c", "printf hi >&2; exit 2"},
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 2 {
		t.Errorf("ExitCode=%d want=2", res.ExitCode)
	}
	if res.Stderr != "hi" {
		t.Errorf("Stderr=%q want=hi", res.Stderr)
	}
	if res.Stdout != "" {
		t.Errorf("Stdout=%q want=empty", res.Stdout)
	}
}

func TestSubprocessRunner_Run_BinaryMissing(t *testing.T) {
	r := SubprocessRunner("/does/not/exist/kit-runner-test-binary")
	_, err := r.Run(context.Background(), Invocation{})
	if err == nil {
		t.Fatal("expected error when binary missing")
	}
}

func TestSubprocessRunner_Run_ContextCancel(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	r := SubprocessRunner(sh)
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel shortly after we know the child is up.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := r.Run(ctx, Invocation{
		Args: []string{"-c", "sleep 30"},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error on ctx cancel")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err=%v want context.Canceled", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Run did not return promptly after cancel: %v", elapsed)
	}
}

func TestSubprocessRunner_Run_Timeout(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	r := SubprocessRunner(sh)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := r.Run(ctx, Invocation{
		Args: []string{"-c", "sleep 30"},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error on timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err=%v want context.DeadlineExceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Run did not return promptly after timeout: %v", elapsed)
	}
}

func TestSubprocessRunner_Stream_Lines(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	r := SubprocessRunner(sh)
	ch := make(chan Event, 16)
	errc := make(chan error, 1)
	go func() {
		errc <- r.Stream(context.Background(), Invocation{
			Args: []string{"-c", "printf 'a\\nb\\nc\\n'; printf 'warn1\\nwarn2\\n' >&2"},
		}, ch)
	}()

	var stdoutLines, stderrLines []string
	var done *Event
collect:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break collect
			}
			switch ev.Kind {
			case "stdout":
				stdoutLines = append(stdoutLines, ev.Data.(string))
			case "stderr":
				stderrLines = append(stderrLines, ev.Data.(string))
			case "done":
				d := ev
				done = &d
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for stream events; got stdout=%v stderr=%v done=%v",
				stdoutLines, stderrLines, done)
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	if !equalStrings(stdoutLines, []string{"a", "b", "c"}) {
		t.Errorf("stdout lines=%v want=[a b c]", stdoutLines)
	}
	if !equalStrings(stderrLines, []string{"warn1", "warn2"}) {
		t.Errorf("stderr lines=%v want=[warn1 warn2]", stderrLines)
	}
	if done == nil {
		t.Fatal("expected terminal done Event")
	}
	res, ok := done.Data.(*Result)
	if !ok {
		t.Fatalf("done.Data type=%T want=*Result", done.Data)
	}
	if res.ExitCode != 0 {
		t.Errorf("done Result.ExitCode=%d want=0", res.ExitCode)
	}
}

func TestSubprocessRunner_Stream_NonZeroExit(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	r := SubprocessRunner(sh)
	ch := make(chan Event, 8)
	errc := make(chan error, 1)
	go func() {
		errc <- r.Stream(context.Background(), Invocation{
			Args: []string{"-c", "exit 3"},
		}, ch)
	}()
	var done *Event
	for ev := range ch {
		if ev.Kind == "done" {
			d := ev
			done = &d
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("Stream err: %v (ExitError should not bubble)", err)
	}
	if done == nil {
		t.Fatal("expected terminal done event")
	}
	res, ok := done.Data.(*Result)
	if !ok {
		t.Fatalf("done.Data type=%T", done.Data)
	}
	if res.ExitCode != 3 {
		t.Errorf("done Result.ExitCode=%d want=3", res.ExitCode)
	}
}

func TestSubprocessRunner_Stream_ContextCancel(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	r := SubprocessRunner(sh)
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan Event, 32)
	errc := make(chan error, 1)
	go func() {
		errc <- r.Stream(ctx, Invocation{
			Args: []string{"-c", "sleep 30"},
		}, ch)
	}()
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	// Drain channel until closed.
	start := time.Now()
	for range ch {
	}
	elapsed := time.Since(start)
	if err := <-errc; err == nil {
		t.Fatal("expected error on ctx cancel")
	} else if !errors.Is(err, context.Canceled) {
		t.Errorf("err=%v want context.Canceled", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Stream did not return promptly after cancel: %v", elapsed)
	}
}

func TestSubprocessRunner_EmptyBinary(t *testing.T) {
	r := SubprocessRunner("")
	if _, err := r.Run(context.Background(), Invocation{}); err == nil {
		t.Error("Run with empty binary should error")
	}
	ch := make(chan Event, 1)
	if err := r.Stream(context.Background(), Invocation{}, ch); err == nil {
		t.Error("Stream with empty binary should error")
	}
}

func equalStrings(a, b []string) bool {
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
