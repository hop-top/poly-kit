//go:build unix

package oauth

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// tryLockFile takes an exclusive non-blocking flock; false when held.
func tryLockFile(f *os.File) (bool, error) {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK):
			return false, nil
		}
		return false, err
	}
}

func unlockFile(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }

// checkLockDir refuses a directory another user could tamper with: not
// owned by the current user, or writable by group or others.
func checkLockDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("oauth: lock directory: %w", ErrTransient)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(st.Uid) != os.Geteuid() || fi.Mode().Perm()&0o022 != 0 {
		return errLockDirUnsafe
	}
	return nil
}
