package cli_test

// E2E coverage for the styled-table contract:
//
//   construct a kit/cli command, render a table through the public
//   output.Render path with output.WithTableStyle(root.TableStyle()),
//   and verify that the TTY path emits ANSI + box-drawing while the
//   non-TTY path emits plain tabwriter output. Content identity is
//   asserted with a stripAnsi helper so the two modes only differ in
//   visual chrome, never in data.
//
// We allocate a real pseudo-terminal pair via creack/pty for the TTY
// path (writerIsTTY in output requires *os.File + isatty.IsTerminal).
// PTY tests are skipped on Windows because the package is unix-only.

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

type e2eRow struct {
	Name   string `table:"Name"`
	Status string `table:"Status"`
	Score  int    `table:"Score"`
}

func e2eRows() []e2eRow {
	return []e2eRow{
		{Name: "alpha", Status: "ok", Score: 1},
		{Name: "beta", Status: "warn", Score: 2},
		{Name: "gamma", Status: "fail", Score: 3},
	}
}

// renderToWriter runs the same code path adopters use: build a Root,
// take its TableStyle, and call output.Render with WithTableStyle. The
// caller supplies the writer so the same call site can be exercised
// against both a PTY (TTY=true) and a bytes.Buffer (TTY=false).
func renderToWriter(t *testing.T, w io.Writer) {
	t.Helper()
	root := cli.New(cli.Config{
		Name:            "stylenant",
		Version:         "0.0.1",
		Short:           "styled-table e2e",
		DisableValidate: true,
	})
	require.NoError(t, output.Render(w, output.Table, e2eRows(),
		output.WithTableStyle(root.TableStyle()),
		output.RowEmphasis(0, output.EmphasisPrimary),
		output.RowEmphasis(2, output.EmphasisMuted),
	))
}

// TestCLIInstallsDefaultTableStyle verifies kit-powered CLIs get themed table
// styling automatically; commands should not need to pass WithTableStyle at
// every Render call.
func TestCLIInstallsDefaultTableStyle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creack/pty is unix-only")
	}

	_ = cli.New(cli.Config{
		Name:            "stylenant",
		Version:         "0.0.1",
		Short:           "styled-table e2e",
		DisableValidate: true,
	})

	master, slave, err := pty.Open()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = master.Close()
		_ = slave.Close()
	})

	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, master)
		done <- buf.Bytes()
	}()

	require.NoError(t, output.Render(slave, output.Table, e2eRows()))
	require.NoError(t, slave.Close())

	tty := string(<-done)
	if !hasANSI(tty) {
		t.Errorf("TTY output missing ANSI escapes: %q", tty)
	}
	if !strings.ContainsRune(tty, '┌') {
		t.Errorf("TTY output missing table border: %q", tty)
	}
}

// TestStyledTable_E2E_TTYAndNonTTYContentIdentity is the headline e2e:
// the same command on a TTY writer emits ANSI + box-drawing, while on
// a non-TTY writer it emits the existing plain tabwriter output, and
// stripping ANSI from the TTY output yields the same cell content.
func TestStyledTable_E2E_TTYAndNonTTYContentIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creack/pty is unix-only")
	}

	// Non-TTY path: plain bytes.Buffer.
	var nontty bytes.Buffer
	renderToWriter(t, &nontty)

	plain := nontty.String()
	if hasANSI(plain) {
		t.Errorf("non-TTY output leaked ANSI escapes: %q", plain)
	}
	for _, r := range []rune{'┌', '─', '│'} {
		if strings.ContainsRune(plain, r) {
			t.Errorf("non-TTY output leaked box-drawing rune %q: %q", r, plain)
		}
	}

	// TTY path: write to the slave end of a real pty pair, drain the
	// master end into a buffer.
	master, slave, err := pty.Open()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = master.Close()
		_ = slave.Close()
	})

	// Drain the master end concurrently — the writer side blocks until
	// the kernel buffer drains.
	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, master)
		done <- buf.Bytes()
	}()

	renderToWriter(t, slave)
	require.NoError(t, slave.Close())

	// Closing slave gives master a clean EOF so the goroutine returns.
	ttyBytes := <-done
	tty := string(ttyBytes)

	if !hasANSI(tty) {
		t.Errorf("TTY output missing ANSI escapes: %q", tty)
	}
	hasBox := false
	for _, r := range []rune{'┌', '─', '│'} {
		if strings.ContainsRune(tty, r) {
			hasBox = true
			break
		}
	}
	if !hasBox {
		t.Errorf("TTY output missing box-drawing characters: %q", tty)
	}

	// Content identity: every row name must appear in both modes
	// (after ANSI stripping for the TTY path). We don't compare byte
	// equality because lipgloss adds borders and column padding.
	stripped := stripANSI(tty)
	for _, r := range e2eRows() {
		if !strings.Contains(plain, r.Name) {
			t.Errorf("plain output missing %q: %q", r.Name, plain)
		}
		if !strings.Contains(stripped, r.Name) {
			t.Errorf("TTY output (stripped) missing %q: %q", r.Name, stripped)
		}
	}
	for _, h := range []string{"Name", "Status", "Score"} {
		if !strings.Contains(plain, h) {
			t.Errorf("plain output missing header %q: %q", h, plain)
		}
		if !strings.Contains(stripped, h) {
			t.Errorf("TTY output (stripped) missing header %q: %q", h, stripped)
		}
	}
}
