// Package kitinit — wizard.go provides the interactive prompt seam used by
// inputs.Gather to collect missing required values. The v1 wizard renders
// plain text (no ANSI colors), so the NO_COLOR env var is a no-op here.
package kitinit

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Wizarder is the interactive-prompt seam used by inputs.Gather.
type Wizarder interface {
	Ask(varName, prompt, defaultValue string, choices []string) (string, error)
}

// TTYWizard prompts on a real TTY (or any io.Reader/Writer pair).
type TTYWizard struct {
	in  *bufio.Reader
	out io.Writer
}

// NewTTYWizard returns a TTYWizard that reads from in and writes to out.
func NewTTYWizard(in io.Reader, out io.Writer) *TTYWizard {
	return &TTYWizard{in: bufio.NewReader(in), out: out}
}

// Ask renders a prompt and returns the user's input.
//   - Text prompts (choices == nil or empty): "<prompt> [<default>]: ".
//     Empty input returns default; non-empty input returns trimmed value.
//   - Choice prompts: prints prompt + numbered list with "*" marking the
//     default; accepts numeric selection (1-based) or exact text match.
//     Loops on invalid input.
func (w *TTYWizard) Ask(varName, prompt, defaultValue string, choices []string) (string, error) {
	if len(choices) == 0 {
		return w.askText(prompt, defaultValue)
	}
	return w.askChoice(prompt, defaultValue, choices)
}

func (w *TTYWizard) askText(prompt, defaultValue string) (string, error) {
	if defaultValue != "" {
		fmt.Fprintf(w.out, "%s [%s]: ", prompt, defaultValue)
	} else {
		fmt.Fprintf(w.out, "%s: ", prompt)
	}
	line, err := w.readLine()
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func (w *TTYWizard) askChoice(prompt, defaultValue string, choices []string) (string, error) {
	for {
		fmt.Fprintln(w.out, prompt+":")
		for i, c := range choices {
			marker := " "
			if c == defaultValue {
				marker = "*"
			}
			fmt.Fprintf(w.out, "  %s %d) %s\n", marker, i+1, c)
		}
		if defaultValue != "" {
			fmt.Fprintf(w.out, "Selection [%s]: ", defaultValue)
		} else {
			fmt.Fprint(w.out, "Selection: ")
		}
		line, err := w.readLine()
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" && defaultValue != "" {
			return defaultValue, nil
		}
		if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(choices) {
			return choices[n-1], nil
		}
		for _, c := range choices {
			if c == line {
				return c, nil
			}
		}
		fmt.Fprintf(w.out, "Invalid selection %q. Try again.\n", line)
	}
}

// readLine reads a single line from the persistent reader. ReadString keeps
// any unread bytes on the reader, so multi-prompt flows (e.g. retry-on-
// invalid-choice) can keep reading subsequent lines from the same input.
func (w *TTYWizard) readLine() (string, error) {
	line, err := w.in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\n"), nil
}
