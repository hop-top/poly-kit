//go:build !windows

package cli

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

// ptyTerminal is a pty pair whose master side the test drives: it records
// every byte the probe writes and, when reply is set, answers each write
// with it.
type ptyTerminal struct {
	slave *os.File

	mu   sync.Mutex
	seen []byte
}

func newPTYTerminal(t *testing.T, reply string) *ptyTerminal {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	pt := &ptyTerminal{slave: slave}
	t.Cleanup(func() { _ = slave.Close(); _ = master.Close() })

	go func() {
		buf := make([]byte, 256)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				pt.mu.Lock()
				pt.seen = append(pt.seen, buf[:n]...)
				pt.mu.Unlock()
				if reply != "" {
					_, _ = io.WriteString(master, reply)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return pt
}

func (p *ptyTerminal) queries() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return bytes.Count(p.seen, []byte(ansi.RequestBackgroundColor))
}

// termState snapshots the terminal attributes; a raw-mode leak shows as
// inequality between two snapshots.
func termState(t *testing.T, f *os.File) term.State {
	t.Helper()
	s, err := term.GetState(f.Fd())
	if err != nil {
		t.Fatalf("term.GetState: %v", err)
	}
	return *s
}

// TestProbeBackground_SilentTerminalQueriedOnce pins that a terminal which
// never answers is asked once and waited on once. lipgloss tries stdin and
// stdout in turn, which on one tty means two queries and two full waits.
func TestProbeBackground_SilentTerminalQueriedOnce(t *testing.T) {
	const wait = 300 * time.Millisecond
	restore := backgroundQueryTimeout
	backgroundQueryTimeout = wait
	defer func() { backgroundQueryTimeout = restore }()

	pt := newPTYTerminal(t, "")

	start := time.Now()
	dark := probeBackground(pt.slave)
	elapsed := time.Since(start)

	if !dark {
		t.Error("probeBackground = false on a silent terminal, want dark default (true)")
	}
	if elapsed < wait {
		t.Errorf("returned after %v, before the %v timeout", elapsed, wait)
	}
	if elapsed >= 2*wait {
		t.Errorf("waited %v, want one timeout (%v): the terminal was queried twice", elapsed, wait)
	}
	// Let the master goroutine drain what the probe wrote.
	time.Sleep(50 * time.Millisecond)
	if got := pt.queries(); got != 1 {
		t.Errorf("OSC 11 queries sent = %d, want 1", got)
	}
}

// TestProbeBackground_AnsweringTerminal pins the detected value and that
// the terminal state is restored afterwards. These guard the inverse
// direction: the single try must still read the verdict the terminal gives.
func TestProbeBackground_AnsweringTerminal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		color string
		want  bool
	}{
		{"dark", "rgb:1010/1010/1010", true},
		{"light", "rgb:f0f0/f0f0/f0f0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pt := newPTYTerminal(t, "\x1b]11;"+tc.color+"\x07\x1b[?62;22c")

			before := termState(t, pt.slave)
			if got := probeBackground(pt.slave); got != tc.want {
				t.Errorf("probeBackground = %v, want %v", got, tc.want)
			}
			if after := termState(t, pt.slave); before != after {
				t.Errorf("terminal state not restored:\n before %+v\n after  %+v", before, after)
			}
			if got := pt.queries(); got != 1 {
				t.Errorf("OSC 11 queries sent = %d, want 1", got)
			}
		})
	}
}

// TestProbeBackground_SilentTerminalRestoresState pins restoration on the
// timeout path.
func TestProbeBackground_SilentTerminalRestoresState(t *testing.T) {
	restore := backgroundQueryTimeout
	backgroundQueryTimeout = 100 * time.Millisecond
	defer func() { backgroundQueryTimeout = restore }()

	pt := newPTYTerminal(t, "")
	before := termState(t, pt.slave)
	_ = probeBackground(pt.slave)
	if after := termState(t, pt.slave); before != after {
		t.Errorf("terminal state not restored after timeout:\n before %+v\n after  %+v", before, after)
	}
}
