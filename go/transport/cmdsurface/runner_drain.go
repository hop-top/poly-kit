package cmdsurface

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// orphanGrace bounds how long the subprocess runner keeps reading a
// child's output once the child has exited and everything it wrote
// has been read. Only a process the child left behind (an orphan
// still holding the pipe) can write after that point; the grace stops
// such a process from holding the invocation open indefinitely.
const orphanGrace = 100 * time.Millisecond

// childPipes carries a child's stdout and stderr through OS pipes the
// runner reads itself. Handing exec.Cmd *os.File ends means os/exec
// starts no copying goroutines of its own, so Cmd.WaitDelay cannot
// race the drain: with an io.Writer, WaitDelay starts when the child
// exits and cuts the pipes if the copy is not done within it, failing
// a successful run whenever the consumer was slow or the reader was
// not scheduled in time.
type childPipes struct {
	stdout, stderr *drainReader
	wOut, wErr     *os.File
}

// newChildPipes creates both pipes and wires their write ends into
// cmd. The caller calls started after cmd.Start, exited after
// cmd.Wait, and close when done reading.
func newChildPipes(cmd *exec.Cmd) (*childPipes, error) {
	rOut, wOut, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("cmdsurface: stdout pipe: %w", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		_ = rOut.Close()
		_ = wOut.Close()
		return nil, fmt.Errorf("cmdsurface: stderr pipe: %w", err)
	}
	stdout, err := newDrainReader(rOut)
	if err == nil {
		var stderr *drainReader
		stderr, err = newDrainReader(rErr)
		if err == nil {
			cmd.Stdout, cmd.Stderr = wOut, wErr
			return &childPipes{stdout: stdout, stderr: stderr, wOut: wOut, wErr: wErr}, nil
		}
	}
	for _, f := range []*os.File{rOut, wOut, rErr, wErr} {
		_ = f.Close()
	}
	return nil, err
}

// started closes the parent's copies of the write ends, so the read
// ends see EOF once the child, and whatever inherited them, closes.
// Also called when Start fails, to release them.
func (p *childPipes) started() {
	_ = p.wOut.Close()
	_ = p.wErr.Close()
}

// exited records that the child has exited; from then on the readers
// stop at the first sign of an orphan holding a pipe open.
func (p *childPipes) exited() {
	p.stdout.exited()
	p.stderr.exited()
}

// close releases the read ends.
func (p *childPipes) close() {
	_ = p.stdout.f.Close()
	_ = p.stderr.f.Close()
}

// waitErr folds a cut pipe into the child's wait error the way
// os/exec reports a WaitDelay overrun: only a run that otherwise
// exited cleanly reports it, as exec.ErrWaitDelay.
func (p *childPipes) waitErr(err error) error {
	if err == nil && (p.stdout.wasCut() || p.stderr.wasCut()) {
		return exec.ErrWaitDelay
	}
	return err
}

// errOrphanCut is returned by a drainReader whose pipe an orphan kept
// open past orphanGrace.
var errOrphanCut = errors.New("cmdsurface: child output held open past exit")
