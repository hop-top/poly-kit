package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// longLineLen is past MaxEventLineBytes by more than one Event's worth
// and not a multiple of it, so the split ends on a short chunk.
const longLineLen = 2*MaxEventLineBytes + 12345

// collectStream runs stream with a channel it drains and returns
// every Event and stream's error. It fails the test, rather than
// waiting on ctx, if the stream does not finish within limit: a
// runner that stops reading leaves an in-process command blocked on
// a write ctx does not interrupt.
func collectStream(t *testing.T, limit time.Duration, stream func(context.Context, chan<- Event) error) ([]Event, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	ch := make(chan Event, 16)
	errc := make(chan error, 1)
	go func() { errc <- stream(ctx, ch) }()

	var evs []Event
	timer := time.NewTimer(limit + 5*time.Second)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return evs, <-errc
			}
			evs = append(evs, ev)
		case <-timer.C:
			t.Fatalf("stream did not finish within %v (%d events so far)", limit, len(evs))
		}
	}
}

// assertSplitLines checks that kind's Events are want's lines, each
// line longer than MaxEventLineBytes split into consecutive Events of
// at most that size.
func assertSplitLines(t *testing.T, evs []Event, kind string, want []string) {
	t.Helper()
	var got []string
	for _, ev := range evs {
		if ev.Kind != kind {
			continue
		}
		s, ok := ev.Data.(string)
		if !ok {
			t.Fatalf("%s Event Data=%T want string", kind, ev.Data)
		}
		if len(s) > MaxEventLineBytes {
			t.Fatalf("%s Event of %d bytes exceeds MaxEventLineBytes", kind, len(s))
		}
		got = append(got, s)
	}
	var wantEvents []string
	for _, line := range want {
		for len(line) > MaxEventLineBytes {
			wantEvents = append(wantEvents, line[:MaxEventLineBytes])
			line = line[MaxEventLineBytes:]
		}
		wantEvents = append(wantEvents, line)
	}
	if len(got) != len(wantEvents) {
		t.Fatalf("%s: %d Events want %d", kind, len(got), len(wantEvents))
	}
	for i := range got {
		if got[i] != wantEvents[i] {
			t.Fatalf("%s Event %d: len=%d want len=%d", kind, i, len(got[i]), len(wantEvents[i]))
		}
	}
}

func doneResult(t *testing.T, evs []Event) *Result {
	t.Helper()
	if len(evs) == 0 || evs[len(evs)-1].Kind != "done" {
		t.Fatal("stream did not end with a done Event")
	}
	res, ok := evs[len(evs)-1].Data.(*Result)
	if !ok || res == nil {
		t.Fatalf("done Data=%T want *Result", evs[len(evs)-1].Data)
	}
	return res
}

func TestSubprocessRunner_Stream_LineLongerThanEventLimit(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	long := strings.Repeat("x", longLineLen)
	script := fmt.Sprintf(
		"echo head; head -c %d /dev/zero | tr '\\000' x; echo; echo tail; echo warn >&2; exit 3",
		longLineLen)

	evs, err := collectStream(t, 20*time.Second, func(ctx context.Context, ch chan<- Event) error {
		return SubprocessRunner(sh).Stream(ctx, Invocation{Path: []string{"-c", script}}, ch)
	})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	assertSplitLines(t, evs, "stdout", []string{"head", long, "tail"})
	assertSplitLines(t, evs, "stderr", []string{"warn"})
	res := doneResult(t, evs)
	if res.ExitCode != 3 {
		t.Errorf("ExitCode=%d want=3", res.ExitCode)
	}
	if want := "head\n" + long + "\ntail\n"; res.Stdout != want {
		t.Errorf("done Stdout len=%d want %d", len(res.Stdout), len(want))
	}
	if res.Truncated {
		t.Error("Truncated=true want=false")
	}
}

func TestInProcessRunner_Stream_LineLongerThanEventLimit(t *testing.T) {
	long := strings.Repeat("y", longLineLen)
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use: "long",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "head")
			fmt.Fprintln(cmd.OutOrStdout(), long)
			fmt.Fprintln(cmd.OutOrStdout(), "tail")
			fmt.Fprint(cmd.ErrOrStderr(), long)
			return errors.New("long failed")
		},
	})

	evs, err := collectStream(t, 20*time.Second, func(ctx context.Context, ch chan<- Event) error {
		return InProcessRunner(root).Stream(ctx, Invocation{Path: []string{"long"}}, ch)
	})
	if err != nil {
		t.Fatalf("Stream err: %v", err)
	}
	assertSplitLines(t, evs, "stdout", []string{"head", long, "tail"})
	assertSplitLines(t, evs, "stderr", []string{long})
	res := doneResult(t, evs)
	if res.ExitCode != 1 {
		t.Errorf("ExitCode=%d want=1", res.ExitCode)
	}
	if want := "head\n" + long + "\ntail\n"; res.Stdout != want {
		t.Errorf("done Stdout len=%d want %d", len(res.Stdout), len(want))
	}
	if res.Stderr != long {
		t.Errorf("done Stderr len=%d want %d", len(res.Stderr), len(long))
	}
}

// TestStreamLines_Splitting pins how streamLines turns bytes into
// Events, with a small limit standing in for MaxEventLineBytes.
func TestStreamLines_Splitting(t *testing.T) {
	tests := []struct {
		name, in string
		want     []string
	}{
		{"lines", "a\nbb\nccc\n", []string{"a", "bb", "ccc"}},
		{"crlf", "a\r\nbb\r\n", []string{"a", "bb"}},
		{"no final newline", "a\nbb", []string{"a", "bb"}},
		{"final cr", "a\nbb\r", []string{"a", "bb"}},
		{"empty lines", "\n\na\n", []string{"", "", "a"}},
		{"exactly the limit", "abcd\n", []string{"abcd"}},
		{"split", "abcdefghij\nk\n", []string{"abcd", "efgh", "ij", "k"}},
		{"split, multiple of limit", "abcdefgh\n", []string{"abcd", "efgh"}},
		{"split, unterminated", "abcdefghij", []string{"abcd", "efgh", "ij"}},
		{"split, crlf at the limit", "abcd\r\nefgh\r\n", []string{"abcd", "efgh"}},
		{"split keeps runes whole", "aé€bc\n", []string{"aé", "€b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan Event, len(tt.in)+1)
			if err := streamLines(strings.NewReader(tt.in), "stdout", 4, ch); err != nil {
				t.Fatalf("err=%v", err)
			}
			close(ch)
			var got []string
			for ev := range ch {
				got = append(got, ev.Data.(string))
			}
			if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", tt.want) {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// TestStreamLines_ReadErrorReturned checks that a read error ends the
// stream with that error, after what was read is delivered.
func TestStreamLines_ReadErrorReturned(t *testing.T) {
	boom := errors.New("boom")
	r := io.MultiReader(strings.NewReader("a\npartial"), &errReader{err: boom})
	ch := make(chan Event, 8)
	err := streamLines(r, "stdout", 4, ch)
	close(ch)
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v want boom", err)
	}
	var got []string
	for ev := range ch {
		got = append(got, ev.Data.(string))
	}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", []string{"a", "part", "ial"}) {
		t.Fatalf("got %q", got)
	}
}

type errReader struct{ err error }

func (e *errReader) Read([]byte) (int, error) { return 0, e.err }

// TestReplayEvents_SplitsLikeARun checks that an idempotent replay
// delivers a long line as the live run delivered it.
func TestReplayEvents_SplitsLikeARun(t *testing.T) {
	long := strings.Repeat("z", longLineLen)
	ch := make(chan Event, 16)
	replayEvents(Result{Stdout: "head\n" + long + "\n", Stderr: "warn"}, ch)
	close(ch)
	var evs []Event
	for ev := range ch {
		evs = append(evs, ev)
	}
	assertSplitLines(t, evs, "stdout", []string{"head", long})
	assertSplitLines(t, evs, "stderr", []string{"warn"})
	doneResult(t, evs)
}
