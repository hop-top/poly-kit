package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"hop.top/kit/go/storage/secret"
)

func errorsIsNotFound(err error) bool { return errors.Is(err, secret.ErrNotFound) }

func defaultLockDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("oauth: no lock directory: %w", ErrTransient)
	}
	return filepath.Join(base, "hop-kit-oauth"), nil
}

// errLockDirUnsafe: the lock directory is not private to the current user,
// so another user could hold or forge the lock. Not transient.
var errLockDirUnsafe = errors.New("oauth: lock directory must be owned by the current user and not writable by group or others")

// lockRef takes the advisory lock for ref: a file named by the SHA-256 of
// ref under dir, held with an OS file lock (flock(2), LockFileEx) so it is
// released if the process dies. It waits until ctx ends. The lock is
// implemented here, not with a lock library, because go/console/cli links
// this package and must not link the serve plane's file-lock module.
func lockRef(ctx context.Context, dir, ref string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("oauth: lock directory: %w", ErrTransient)
	}
	if err := checkLockDir(dir); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(ref))
	f, err := os.OpenFile(filepath.Join(dir, hex.EncodeToString(sum[:])+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("oauth: lock file: %w", ErrTransient)
	}
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		ok, err := tryLockFile(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("oauth: lock file: %w", ErrTransient)
		}
		if ok {
			return func() { unlockFile(f); f.Close() }, nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("oauth: waiting for refresh lock: %w: %w", ErrTransient, ctx.Err())
		case <-t.C:
		}
	}
}
