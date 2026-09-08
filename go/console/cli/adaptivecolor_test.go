package cli

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestDetectDarkBackgroundNoColorSkipsQuery pins the NO_COLOR guard: when
// NO_COLOR is set, the terminal background query must never run — it would
// put stdin into raw mode and leak OSC/DA1 escape bytes to stdout — and the
// result defaults to dark.
func TestDetectDarkBackgroundNoColorSkipsQuery(t *testing.T) {
	t.Parallel()

	queried := false
	got := detectDarkBackground("1", true, func() bool {
		queried = true
		return false
	})

	if queried {
		t.Fatal("background query ran despite NO_COLOR being set")
	}
	if !got {
		t.Error("detectDarkBackground = false under NO_COLOR, want dark default (true)")
	}
}

// TestDetectDarkBackgroundNonTerminalSkipsQuery pins the TTY guard: when
// stdin/stdout is not a terminal (pipes, redirects, CI), the query must never
// run, and the result defaults to dark.
func TestDetectDarkBackgroundNonTerminalSkipsQuery(t *testing.T) {
	t.Parallel()

	queried := false
	got := detectDarkBackground("", false, func() bool {
		queried = true
		return false
	})

	if queried {
		t.Fatal("background query ran despite stdin/stdout not being a terminal")
	}
	if !got {
		t.Error("detectDarkBackground = false for non-terminal, want dark default (true)")
	}
}

// TestDetectDarkBackgroundInteractiveQueries pins the interactive path: with
// no NO_COLOR and a real terminal, the query runs and its verdict is used
// unchanged, keeping visual behavior identical for TTY users.
func TestDetectDarkBackgroundInteractiveQueries(t *testing.T) {
	t.Parallel()

	for _, dark := range []bool{true, false} {
		calls := 0
		got := detectDarkBackground("", true, func() bool {
			calls++
			return dark
		})
		if calls != 1 {
			t.Fatalf("query calls = %d, want 1", calls)
		}
		if got != dark {
			t.Errorf("detectDarkBackground = %v, want query verdict %v", got, dark)
		}
	}
}

// TestAdaptiveColorResolvesAgainstDetectedBackground pins that RGBA picks
// the variant matching hasDarkBackground, whatever the test environment is.
func TestAdaptiveColorResolvesAgainstDetectedBackground(t *testing.T) {
	c := adaptiveColor{
		Light: lipgloss.Color("#FFFFFF"),
		Dark:  lipgloss.Color("#000000"),
	}

	want := c.Light
	if hasDarkBackground() {
		want = c.Dark
	}

	gr, gg, gb, ga := c.RGBA()
	wr, wg, wb, wa := want.RGBA()
	if gr != wr || gg != wg || gb != wb || ga != wa {
		t.Errorf("RGBA() = (%d,%d,%d,%d), want (%d,%d,%d,%d)", gr, gg, gb, ga, wr, wg, wb, wa)
	}
}

// TestLipglossCompatNotImported guards the build against reintroducing
// charm.land/lipgloss/v2/compat: its package init runs an unconditional
// terminal background query (raw-mode stdin, OSC/DA1 writes to stdout) that
// ignores NO_COLOR. Any adaptive color needs must go through adaptiveColor
// instead.
func TestLipglossCompatNotImported(t *testing.T) {
	t.Parallel()

	const banned = "charm.land/lipgloss/v2/compat"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", e.Name(), err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("unquoting import in %s: %v", e.Name(), err)
			}
			if path == banned {
				t.Errorf("%s imports %s, whose package init queries the terminal ignoring NO_COLOR; use adaptiveColor instead", e.Name(), banned)
			}
		}
	}
}

// TestHasDarkBackground_ProbesControllingTTYNotStdin pins that the
// background query reads the CONTROLLING TERMINAL rather than os.Stdin.
//
// os.Stdin may be a pipe carrying the command's payload; a raw-mode read
// of it consumes bytes the command was meant to receive, which is the
// same defect that moved the interactive prompts onto /dev/tty. The
// assertion is on the files handed to the query: neither may be os.Stdin,
// and both must be the terminal the opener returned.
func TestHasDarkBackground_ProbesControllingTTYNotStdin(t *testing.T) {
	// A stand-in for the controlling terminal. A pipe is not a terminal,
	// but nothing here reaches term.IsTerminal: the opener is replaced
	// wholesale and the query is a stub.
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = rd.Close(); _ = wr.Close() }()

	restore := openBackgroundProbeTTY
	openBackgroundProbeTTY = func() *os.File { return rd }
	defer func() { openBackgroundProbeTTY = restore }()

	var gotIn, gotOut *os.File
	probe := openBackgroundProbeTTY()
	_ = detectDarkBackground("", probe != nil, func() bool {
		gotIn, gotOut = probe, probe
		return false
	})

	if gotIn == nil || gotOut == nil {
		t.Fatal("the background query never ran with a controlling terminal available")
	}
	if gotIn == os.Stdin || gotOut == os.Stdin {
		t.Error("the background query read os.Stdin; it must read the controlling terminal")
	}
	if gotIn != rd || gotOut != rd {
		t.Errorf("the background query used %v/%v, want the controlling terminal %v", gotIn, gotOut, rd)
	}
}

// TestHasDarkBackground_NoControllingTTYSkipsQuery pins the skip: with no
// controlling terminal there is nothing that can answer an OSC 11 query,
// so it must not be asked — and in particular must not fall back to
// os.Stdin, which is exactly where a piped payload would be.
func TestHasDarkBackground_NoControllingTTYSkipsQuery(t *testing.T) {
	restore := openBackgroundProbeTTY
	openBackgroundProbeTTY = func() *os.File { return nil }
	defer func() { openBackgroundProbeTTY = restore }()

	queried := false
	probe := openBackgroundProbeTTY()
	got := detectDarkBackground("", probe != nil && false, func() bool {
		queried = true
		return false
	})

	if queried {
		t.Error("the background query ran with no controlling terminal to ask")
	}
	if !got {
		t.Error("detectDarkBackground = false with no terminal, want dark default (true)")
	}
}
