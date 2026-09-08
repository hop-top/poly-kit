package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/mattn/go-isatty"
)

// Interactive prompts read and write the controlling terminal, never
// the command's stdin.
//
// A prompt that reads cmd.InOrStdin() consumes the command's payload.
// `tool import - < data.json` under a prompted confirm loses whatever
// bufio buffered past the answer's newline — a 4096-byte fill, so a
// payload under 4KB vanishes entirely and larger ones are silently
// truncated, both at exit 0.
//
// /dev/tty is the controlling terminal regardless of how stdin and
// stdout are redirected, which is the property needed: the question
// reaches the operator and the payload reaches the command. Output
// goes to the terminal too, not to stderr, because stderr may be
// redirected and a prompt nobody can see is a hang with no
// explanation.
//
// Interactivity is decided by whether that open succeeds, not by
// isatty on stdin. One condition covers CI, `< /dev/null` and piped
// stdin, and it is the honest question: is there anyone to ask.
// When there is not, callers fall back to their non-interactive
// decision rather than blocking. A hang is worse than a refusal.

// PromptTTY is the controlling-terminal handle used by every
// interactive prompt in kit.
//
// Nil means no terminal: no controlling tty, or one that opened but
// is not a terminal. Callers MUST treat nil as "nobody to ask" and
// take their non-interactive path.
type PromptTTY struct {
	// sink receives the question. The real terminal is both sink and
	// answer source; a test source may split them.
	sink   io.Writer
	reader *bufio.Reader
}

// Write sends the question to the terminal.
func (t *PromptTTY) Write(p []byte) (int, error) {
	if t.sink == nil {
		return len(p), nil
	}
	return t.sink.Write(p)
}

// ReadLine reads one line of the operator's answer, newline trimmed.
//
// The bufio.Reader is retained on the PromptTTY rather than
// constructed per call. A fresh reader per prompt discards whatever
// it buffered past the newline, so a second prompt on the same
// terminal would read bytes that the first already swallowed.
func (t *PromptTTY) ReadLine() (string, error) {
	line, err := t.reader.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// promptTTYOnce guards the process-lifetime terminal handle.
//
// The handle is opened once and retained: reopening /dev/tty per
// prompt would give each prompt a fresh buffer and reintroduce the
// byte loss between prompts.
var (
	promptTTYOnce sync.Once
	promptTTY     *PromptTTY
)

// openPromptTTYFn opens the controlling terminal. Overridable in
// tests, which have no terminal of their own. Returns nil when there
// is no terminal to prompt on.
var openPromptTTYFn = func() *PromptTTY {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	if !isatty.IsTerminal(f.Fd()) {
		// A /dev/tty that opened but is not a terminal is not
		// something to prompt on. Close it rather than leak it.
		_ = f.Close()
		return nil
	}
	return newPromptTTY(f)
}

// newPromptTTY wraps an already-open terminal handle.
func newPromptTTY(f *os.File) *PromptTTY {
	return &PromptTTY{sink: f, reader: bufio.NewReader(f)}
}

// ControllingTTY returns the process-wide prompt terminal, or nil
// when there is none.
//
// The handle is opened on first use and retained for the process
// lifetime, so successive prompts share one buffer and no answer is
// lost between them. It is deliberately never closed: the prompt is
// interactive by definition and the process exits soon after.
func ControllingTTY() *PromptTTY {
	promptTTYOnce.Do(func() { promptTTY = openPromptTTYFn() })
	return promptTTY
}

// PromptSource supplies the terminal that interactive prompts use.
//
// Adopters and test harnesses install one via [WithPromptSource] to
// redirect a prompt without patching kit and without feeding the
// command's stdin to both the prompt and the payload. Returning nil
// means non-interactive: prompts fall back to their refusal path.
type PromptSource func() *PromptTTY

// PromptSourceFromFile builds a PromptSource over an already-open
// file, for adopters driving prompts from a pty or a fixture.
func PromptSourceFromFile(f *os.File) PromptSource {
	if f == nil {
		return func() *PromptTTY { return nil }
	}
	tty := newPromptTTY(f)
	return func() *PromptTTY { return tty }
}

// PromptSourceFromReadWriter builds a PromptSource over an in-memory
// answer script and a sink for the questions. Intended for tests and
// conformance harnesses that have no terminal at all.
func PromptSourceFromReadWriter(r io.Reader, w io.Writer) PromptSource {
	tty := &PromptTTY{sink: w, reader: bufio.NewReader(r)}
	return func() *PromptTTY { return tty }
}

// WithPromptSource installs the prompt terminal source on the root.
// Unset means the real controlling terminal.
func WithPromptSource(src PromptSource) func(*Root) {
	return func(r *Root) {
		r.promptSource = src
	}
}

// globalPromptSource overrides the prompt terminal process-wide.
//
// The per-root WithPromptSource is the option adopters want. This
// exists for harnesses that hold a *cobra.Command rather than the
// *Root the option applies to, and so cannot reach the field.
var globalPromptSource PromptSource

// InstallGlobalPromptSource overrides the prompt terminal for every
// root in the process and returns a restore callback.
//
// Intended for conformance harnesses and adopter tests that need to
// answer a prompt without a real terminal. Prefer WithPromptSource
// wherever the *Root is in hand: this is process-wide state and is
// not safe to install from parallel tests.
func InstallGlobalPromptSource(src PromptSource) func() {
	prev := globalPromptSource
	globalPromptSource = src
	return func() { globalPromptSource = prev }
}

// promptAsk writes question to the terminal and reads one answer
// line. Returns asked=false when there is no terminal, which is the
// caller's signal to take its non-interactive path rather than block.
//
// EOF on the terminal (^D) reads as a blank answer: the operator was
// asked and chose to end it. Callers whose blank default is "yes" want
// [promptAskEOF] instead, which keeps the two apart.
func promptAsk(src PromptSource, question string) (answer string, asked bool) {
	answer, asked, _ = promptAskEOF(src, question)
	return answer, asked
}

// promptAskEOF is promptAsk plus the ended-at-EOF signal.
//
// A bare Enter and a ^D both yield a blank answer, and for a [y/N]
// question the two collapse harmlessly. They do not collapse when the
// blank default is yes: Enter means "take the offer", while ^D means
// the operator walked away and must not be read as consent.
func promptAskEOF(src PromptSource, question string) (answer string, asked, eof bool) {
	if src == nil {
		src = globalPromptSource
	}
	if src == nil {
		src = ControllingTTY
	}
	tty := src()
	if tty == nil {
		return "", false, false
	}
	fmt.Fprint(tty, question)
	line, err := tty.ReadLine()
	if err != nil && line == "" {
		return "", true, true
	}
	return strings.ToLower(strings.TrimSpace(line)), true, false
}
