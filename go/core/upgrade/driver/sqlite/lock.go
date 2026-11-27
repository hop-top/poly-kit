package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// lockFileName is the advisory lock file, in the version file's directory.
const lockFileName = ".lock"

// lockPoll is how often a blocked Lock retries.
const lockPoll = 50 * time.Millisecond

// Lock takes an exclusive advisory file lock (flock) on
// <version dir>/.lock, creating the directory if needed, and blocks
// until it is held or ctx is done (then it returns ctx's error and holds
// nothing). The lock excludes other processes and other drivers in this
// process. The file is never deleted. The returned unlock releases it.
func (d *Driver) Lock(ctx context.Context) (func() error, error) {
	dir, err := d.stateDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	fl := flock.New(filepath.Join(dir, lockFileName))
	ok, err := fl.TryLockContext(ctx, lockPoll)
	if err != nil || !ok {
		_ = fl.Unlock()
		if err == nil {
			err = fmt.Errorf("not acquired")
		}
		return nil, fmt.Errorf("lock %s: %w", fl.Path(), err)
	}
	return fl.Unlock, nil
}
