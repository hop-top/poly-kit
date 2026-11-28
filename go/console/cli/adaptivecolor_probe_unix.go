//go:build !windows

package cli

import (
	"errors"
	"image/color"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// backgroundQueryTimeout bounds the wait for a terminal reply. Matches
// lipgloss's own wait; a var so tests need not sit through it.
var backgroundQueryTimeout = 2 * time.Second

// probeBackground reports whether the terminal behind f has a dark
// background, defaulting to dark when it does not answer.
//
// lipgloss.HasDarkBackground is not used: given one tty as both input and
// output it queries that tty twice, so a terminal that never answers costs
// two full timeouts and receives the query twice. This is lipgloss's single
// try: raw mode, OSC 11 plus DA1, read until the DA1 reply or the timeout,
// restore. The wait is a select on the fd (poll(2) reports POLLNVAL for /dev/tty on macOS), not a goroutine, so the terminal
// is restored on every path.
func probeBackground(f *os.File) bool {
	c := queryBackground(f, backgroundQueryTimeout)
	if c == nil {
		return true
	}
	r, g, b, _ := c.RGBA()
	hi, lo := max(r, g, b), min(r, g, b)
	return float64(hi+lo)/(2*0xffff) < 0.5 // HSL lightness, as lipgloss
}

func queryBackground(f *os.File, timeout time.Duration) (c color.Color) {
	fd := f.Fd()
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil
	}
	defer term.Restore(fd, state) //nolint:errcheck

	if _, err := f.WriteString(ansi.RequestBackgroundColor + ansi.RequestPrimaryDeviceAttributes); err != nil {
		return nil
	}

	pa := ansi.GetParser()
	defer ansi.PutParser(pa)
	var (
		deadline = time.Now().Add(timeout)
		seq      []byte
		in       [256]byte
		pstate   byte
	)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return c
		}
		var rs unix.FdSet
		rs.Set(int(fd))
		tv := unix.NsecToTimeval(left.Nanoseconds())
		n, err := unix.Select(int(fd)+1, &rs, nil, nil, &tv)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n == 0 {
			return c
		}
		n, err = unix.Read(int(fd), in[:])
		if err != nil || n <= 0 {
			return c
		}
		for p := in[:n]; len(p) > 0; {
			s, _, read, next := ansi.DecodeSequence(p, pstate, pa)
			seq = append(seq, s...)
			p, pstate = p[read:], next
			if next != ansi.NormalState {
				continue
			}
			switch {
			case ansi.HasOscPrefix(string(seq)) && pa.Command() == 11:
				if parts := strings.Split(string(pa.Data()), ";"); len(parts) == 2 {
					c = ansi.XParseColor(parts[1])
				}
			case ansi.HasCsiPrefix(string(seq)) && pa.Command() == ansi.Command('?', 0, 'c'):
				return c
			}
			seq = seq[:0]
		}
	}
}
