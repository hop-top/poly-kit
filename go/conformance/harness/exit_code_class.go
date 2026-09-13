package harness

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/output/envelope"
)

// The table this file used to carry has been deleted. It was a
// hand-maintained copy of kit's exit-code table, refreshed by a comment
// asking whoever grew the kit-side table to remember this one. They did
// not: CONSENT_REFUSED (7) and PREREQUISITE (70) were added upstream and
// never copied, and the 66-69 band was never included at all. Because
// the lookup defaulted to 1 on a miss, a leaf declaring
// kit/exit-codes=PREREQUISITE was asserted against exit 1 — a wrong
// assertion that reads as a real failure.
//
// The copy was justified by keeping the harness off kit/output when
// adopters use it in isolation. That justification did not survive
// contact with the import graph: this package already imports
// hop.top/kit/go/console/cli, which pulls console/output and lipgloss
// transitively. There was no isolation left to protect. The envelope
// package the table now comes from is a leaf — stdlib plus the YAML
// encoder — so reading the real table costs nothing the harness was not
// already paying.

// bandClassToCode carries the tool-specific >6 slots the conformance
// tree owns. They are not in envelope's table by design: they are
// declared by the packages that own them, and putting them in the leaf
// would invert the dependency the leaf exists to avoid.
//
// Values are asserted against envelope.ExtensionBand in the drift guard,
// so a slot renumbered on either side fails a test rather than silently
// double-booking a number.
var bandClassToCode = map[string]int{
	"LEAK_DETECTED":    66,
	"CONFIG":           67,
	"GRADE_FAIL":       68,
	"GRADE_UNGRADABLE": 69,
}

// ClassToExitCode resolves a kit exit-class symbol to its numeric code,
// defaulting to GENERIC (1) on unknown class names.
//
// The default is retained rather than surfaced because an adopter may
// legitimately declare a class of its own in kit/exit-codes, and the
// harness cannot know that adopter's table. What changed is that a class
// kit itself defines no longer falls through to it.
func ClassToExitCode(class string) int {
	norm := strings.ToUpper(strings.TrimSpace(class))
	if c, ok := envelope.ExitCodeForClass(norm); ok {
		return c
	}
	if c, ok := bandClassToCode[norm]; ok {
		return c
	}
	return 1
}

// exitCodeToClass returns a human-readable class name for an
// observed numeric exit code. Used in failure messages.
func exitCodeToClass(code int) string {
	if class, ok := envelope.ClassForExitCode(code); ok {
		return class
	}
	for class, n := range bandClassToCode {
		if n == code {
			return class
		}
	}
	return "UNKNOWN"
}

// AssertExitCodeClass runs cmd and asserts the observed exit code
// falls in the declared exit-code class set.
//
// The expected class is read from the leaf's kit/exit-codes
// annotation (comma-separated class names). Adopters override at
// the call site via harness.WithExpectedClass(...). When neither
// annotation nor option is present, the harness defaults to
// expecting OK and surfaces a hint in the failure message.
func AssertExitCodeClass(t TB, cmd *cobra.Command, opts ...Option) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("AssertExitCodeClass: cmd is nil")
		return
	}
	c := apply(opts)
	leaf := resolveLeaf(cmd, c.args)

	classes := c.expectedClass
	defaulted := false
	if len(classes) == 0 {
		if leaf != nil && leaf.Annotations != nil {
			if raw := leaf.Annotations["kit/exit-codes"]; raw != "" {
				classes = splitCSV(raw)
			}
		}
		if len(classes) == 0 {
			classes = []string{"OK"}
			defaulted = true
		}
	}

	res := runCaptured(c, cmd)
	if exitMatches(res.exitCode, classes) {
		return
	}

	want := make([]string, 0, len(classes))
	for _, cl := range classes {
		want = append(want, fmt.Sprintf("%s(%d)", cl, ClassToExitCode(cl)))
	}
	msg := fmt.Sprintf(
		"AssertExitCodeClass: exit code %d not in declared class set {%s}\n\n  cmd: %s\n  expected: %s\n  observed: %d (%s)",
		res.exitCode,
		strings.Join(classes, ","),
		commandLine(cmd, c.args),
		strings.Join(want, " | "),
		res.exitCode,
		exitCodeToClass(res.exitCode),
	)
	if res.stderr.Len() > 0 {
		msg += "\n\n  stderr: " + truncate(res.stderr.String(), 500)
	}
	if defaulted {
		msg += "\n\nhint: leaf has no kit/exit-codes annotation; defaulted to expecting OK." +
			"\n      Either set cmd.Annotations[\"kit/exit-codes\"] at registration time or" +
			"\n      pass harness.WithExpectedClass(...) explicitly."
	}
	t.Errorf("%s", msg)
}

// exitMatches reports whether code is in any class's numeric value.
func exitMatches(code int, classes []string) bool {
	for _, cl := range classes {
		if ClassToExitCode(cl) == code {
			return true
		}
	}
	return false
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// commandLine renders "name arg arg..." for failure messages.
func commandLine(cmd *cobra.Command, args []string) string {
	name := ""
	if cmd != nil {
		name = cmd.Name()
	}
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}
