//go:build unix

package flagerrexit_test

import (
	"os"
	"os/exec"
	"syscall"
)

// setControllingTTY makes tty the child's controlling terminal, so
// /dev/tty inside the child resolves to it.
//
// Setsid puts the child in a new session with no controlling terminal;
// Setctty then claims the fd at Ctty as that session's terminal. Both are
// required: without Setsid the child inherits the test runner's session
// (where /dev/tty is whatever `go test` was launched from, if anything),
// and without Setctty the pty is just another open file.
//
// Ctty is indexed against the child's fd table, where 0/1/2 are the
// std streams the exec.Cmd fields set, so 3 is the first entry ExtraFiles
// contributes.
func setControllingTTY(cmd *exec.Cmd, tty *os.File) {
	cmd.ExtraFiles = append(cmd.ExtraFiles, tty)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    3,
	}
}
