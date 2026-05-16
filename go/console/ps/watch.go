package ps

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mattn/go-isatty"
)

// clearScreen writes ANSI escape codes to move cursor home and clear.
const clearScreen = "\033[H\033[2J"

// separator printed between watch iterations on non-TTY writers.
const watchSeparator = "---"

// isTTY reports whether w is connected to a terminal.
func isTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
	}
	return false
}

// runWatch polls the provider at interval, clearing and redrawing.
// It writes to w instead of os.Stdout. If w is not a TTY, a separator
// line is printed between iterations instead of ANSI clear-screen.
func runWatch(
	ctx context.Context,
	w io.Writer,
	p Provider,
	format string,
	noColor bool,
	all bool,
	interval time.Duration,
) error {
	tty := isTTY(w)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Render immediately, then on each tick.
	if err := watchOnce(ctx, w, p, format, noColor, all, tty, true); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := watchOnce(ctx, w, p, format, noColor, all, tty, false); err != nil {
				return err
			}
		}
	}
}

func watchOnce(
	ctx context.Context,
	w io.Writer,
	p Provider,
	format string,
	noColor bool,
	all bool,
	tty bool,
	first bool,
) error {
	entries, err := p.List(ctx)
	if err != nil {
		return err
	}

	if !all {
		entries = filterActive(entries)
	}

	var buf bytes.Buffer
	if err := Render(&buf, entries, format, noColor); err != nil {
		return err
	}

	if tty {
		fmt.Fprint(w, clearScreen)
	} else if !first {
		fmt.Fprintln(w, watchSeparator)
	}

	_, err = buf.WriteTo(w)
	return err
}
