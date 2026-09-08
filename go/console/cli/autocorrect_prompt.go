package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// The autocorrect prompt reads /dev/tty, NOT stdin.
//
// This is the one place in kit that must not reuse promptConfirm from
// policy_runE.go. That helper reads cmd.InOrStdin(), which is stdin, and
// for the confirm gate that has been survivable: a destructive command
// whose stdin is a data stream is rare. The autocorrect prompt does not
// get that luxury. It fires on a MISTYPED FLAG, which is orthogonal to
// what the command does with stdin, so `tool import - --fmt=json` with a
// heredoc on stdin would have its first line eaten as the answer to a
// question about a flag name. The data would be silently truncated and
// the answer would be whatever the payload happened to start with.
//
// /dev/tty is the controlling terminal regardless of how stdin is
// redirected, which is exactly the property needed: the question reaches
// the operator and the payload reaches the command. When there is no
// controlling terminal to open, there is no one to ask, and the caller
// falls back to suggest-only rather than blocking.

// autocorrectTTYFn opens the controlling terminal for the prompt.
// Overridable in tests, which have no tty of their own. Returns nil when
// no controlling terminal is available.
var autocorrectTTYFn = func() *os.File {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	if !isatty.IsTerminal(f.Fd()) {
		// A /dev/tty that opened but is not a terminal is not something
		// to prompt on. Close it rather than leaking the handle.
		_ = f.Close()
		return nil
	}
	return f
}

// promptAutocorrect asks whether to apply the correction, reading the
// answer from the controlling terminal.
//
// Returns (applied, asked). asked is false when there was no terminal to
// ask on, which is the non-TTY fallback: the caller then behaves exactly
// as it would under AutocorrectOff. Distinguishing "declined" from "never
// asked" matters because only the former is a decision the operator made.
//
// defaultYes chooses what a bare Enter means. It is true only for a
// read-only leaf; anything that mutates requires the operator to type y.
func promptAutocorrect(typed, corrected string, defaultYes bool) (applied, asked bool) {
	tty := autocorrectTTYFn()
	if tty == nil {
		return false, false
	}
	defer func() { _ = tty.Close() }()

	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}
	// The question goes to the terminal too, not to stderr: stderr may be
	// redirected to a file, and a prompt written somewhere the operator
	// cannot see it is a hang with no explanation.
	fmt.Fprintf(tty, "Did you mean --%s instead of --%s? %s ", corrected, typed, hint)

	line, err := bufio.NewReader(tty).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if err != nil && answer == "" {
		// EOF on the terminal (^D) is a decline, not an unanswered
		// question: the operator was asked and chose to end it.
		return false, true
	}
	switch answer {
	case "":
		return defaultYes, true
	case "y", "yes":
		return true, true
	}
	return false, true
}
