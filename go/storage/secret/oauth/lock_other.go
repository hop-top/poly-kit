//go:build !unix && !windows

package oauth

import (
	"errors"
	"os"
)

func tryLockFile(*os.File) (bool, error) {
	return false, errors.New("oauth: file locks unsupported on this platform")
}

func unlockFile(*os.File) {}

func checkLockDir(string) error { return nil }
