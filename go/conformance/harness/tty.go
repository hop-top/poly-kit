package harness

import (
	"bytes"
	"io"
)

// installTTY swaps in a simulated terminal for the duration of one
// invocation.
//
//   - When WithTTY() is set, the harness installs a kit prompt
//     source standing in for the controlling terminal. Prompts read
//     the answers from WithPromptAnswers (empty = immediate EOF,
//     which prompts read as a decline) and write their questions to
//     a discard sink.
//   - When NonTTY() (the default), no installation happens: tests
//     inherit go test's terminal-less environment, where a prompt
//     finds nothing to ask on and takes its refusal path.
//
// The prompt terminal is deliberately NOT the command's stdin. That
// separation is the point: a harness that fed one reader to both
// could not tell a consumed payload from a delivered one.
func (c *config) installTTY() func() {
	if !c.withTTY {
		return func() {}
	}
	answers := c.promptAnswers
	if answers == nil {
		answers = bytes.NewReader(nil)
	}
	if ttyProbeFn != nil {
		return ttyProbeFn(true)
	}
	if installPromptSourceFn == nil {
		return func() {}
	}
	return installPromptSourceFn(answers, io.Discard)
}

// ttyProbeFn is the optional kit-side seam install function. Nil
// when the kit binary in use does not expose a TTY probe. Set via
// init() in a future kit version; tests can override too.
//
// The function takes the desired probe value (true = "we have a
// tty") and returns a restore callback that undoes the install.
var ttyProbeFn func(value bool) func()

// installPromptSourceFn installs a stand-in prompt terminal reading
// answers from r and writing questions to w, returning a restore
// callback. Wired to the kit prompt seam in promptsource.go.
var installPromptSourceFn func(r io.Reader, w io.Writer) func()
