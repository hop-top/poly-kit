//go:build !windows

package cmdsurface

import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

// drainReader reads one child output pipe to EOF. Once the child has
// exited it reads on only while the pipe has data: everything the
// child wrote is in the pipe by then, so the first empty read after
// exit proves the child's output is fully read, and only an orphan
// can still write. From that point the reader waits at most
// orphanGrace for EOF before giving up with errOrphanCut. An orphan
// that keeps the pipe from ever reading empty is stopped instead by
// orphanByteCap on what is read after exit.
//
// The proof does not depend on timing: however late this goroutine
// is scheduled, it never stops while the child's output is unread.
type drainReader struct {
	f  *os.File
	rc syscall.RawConn

	mu    sync.Mutex
	gone  bool // the child has exited
	grace bool // drained after exit; the orphan deadline is armed
	cut   bool

	afterExit int // bytes read since the child was seen gone
}

func newDrainReader(f *os.File) (*drainReader, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return nil, err
	}
	return &drainReader{f: f, rc: rc}, nil
}

// exited marks the child gone and wakes a reader parked on an empty
// pipe, so it re-checks the pipe under the new state.
func (d *drainReader) exited() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gone = true
	if !d.grace {
		_ = d.f.SetReadDeadline(time.Now())
	}
}

func (d *drainReader) wasCut() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cut
}

func (d *drainReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		var n int
		var rerr error
		drained, gone := false, false
		err := d.rc.Read(func(fd uintptr) bool {
			var grace bool
			d.mu.Lock()
			gone, grace = d.gone, d.grace
			d.mu.Unlock()
			for {
				n, rerr = syscall.Read(int(fd), p)
				if !errors.Is(rerr, syscall.EINTR) {
					break
				}
			}
			if errors.Is(rerr, syscall.EAGAIN) {
				if gone && !grace {
					drained = true
					return true
				}
				return false // wait for data, EOF or the deadline
			}
			return true
		})
		switch {
		case err != nil && errors.Is(err, os.ErrDeadlineExceeded):
			d.mu.Lock()
			if d.grace {
				d.cut = true
				d.mu.Unlock()
				return 0, errOrphanCut
			}
			// Woken by exited: clear its deadline and look again.
			_ = d.f.SetReadDeadline(time.Time{})
			d.mu.Unlock()
		case err != nil:
			return 0, err
		case drained:
			d.mu.Lock()
			d.grace = true
			_ = d.f.SetReadDeadline(time.Now().Add(orphanGrace))
			d.mu.Unlock()
		case rerr != nil:
			return 0, rerr
		case n == 0:
			return 0, io.EOF
		case gone:
			d.mu.Lock()
			d.afterExit += n
			if d.afterExit > orphanByteCap {
				d.cut = true
				d.mu.Unlock()
				return 0, errOrphanCut
			}
			d.mu.Unlock()
			return n, nil
		default:
			return n, nil
		}
	}
}
