package cli

import (
	"fmt"
)

// The autocorrect prompt reads the controlling terminal, NOT stdin.
//
// It must not read cmd.InOrStdin(). The prompt fires on a MISTYPED FLAG,
// which is orthogonal to what the command does with stdin, so
// `tool import - --fmt=json` with a heredoc on stdin would have its first
// line eaten as the answer to a question about a flag name. The data would
// be silently truncated and the answer would be whatever the payload
// happened to start with.
//
// The terminal handle comes from the shared prompt helper in ttyprompt.go,
// which every interactive prompt in kit uses: one /dev/tty open per
// process, one retained bufio.Reader. Opening and closing a terminal per
// prompt would give each prompt a fresh 4096-byte buffer and lose whatever
// the previous prompt read past its newline — the autocorrect prompt and
// the confirm gate can both fire in a single invocation.
//
// When there is no controlling terminal to open, there is no one to ask,
// and the caller falls back to suggest-only rather than blocking.

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
func promptAutocorrect(src PromptSource, typed, corrected string, defaultYes bool) (applied, asked bool) {
	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}
	// The question goes to the terminal too, not to stderr: stderr may be
	// redirected to a file, and a prompt written somewhere the operator
	// cannot see it is a hang with no explanation.
	answer, asked, eof := promptAskEOF(src, fmt.Sprintf(
		"Did you mean --%s instead of --%s? %s ", corrected, typed, hint))
	if !asked {
		return false, false
	}
	if eof {
		// EOF on the terminal (^D) is a decline, not an unanswered
		// question: the operator was asked and chose to end it. Never
		// the yes default, which belongs to a deliberate Enter.
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
