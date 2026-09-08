//go:build !unix

package flagerrexit_test

import (
	"os"
	"os/exec"
)

// setControllingTTY is a no-op where the session/controlling-terminal
// model does not exist. The pty-based tests skip on the platforms this
// build tag covers, because pty.Open itself fails there.
func setControllingTTY(_ *exec.Cmd, _ *os.File) {}
