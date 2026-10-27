//go:build windows

package cmdsurface

import (
	"os"
	"sync"
	"time"
)

// drainReader reads one child output pipe to EOF. Windows pipes are
// not pollable, so the drain cannot prove the child's output is read
// before bounding an orphan: once the child exits the reader gets
// orphanGrace, then the pipe is closed and reading stops with
// errOrphanCut.
type drainReader struct {
	f *os.File

	mu    sync.Mutex
	timer *time.Timer
	cut   bool
	eof   bool
}

func newDrainReader(f *os.File) (*drainReader, error) {
	return &drainReader{f: f}, nil
}

func (d *drainReader) exited() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.timer = time.AfterFunc(orphanGrace, func() {
		d.mu.Lock()
		if !d.eof {
			d.cut = true
		}
		d.mu.Unlock()
		_ = d.f.Close()
	})
}

func (d *drainReader) wasCut() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cut
}

func (d *drainReader) Read(p []byte) (int, error) {
	n, err := d.f.Read(p)
	if err != nil {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.cut {
			return n, errOrphanCut
		}
		d.eof = true
		if d.timer != nil {
			d.timer.Stop()
		}
	}
	return n, err
}
