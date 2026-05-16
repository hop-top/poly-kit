package harness

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// exitClassToCode mirrors the kit/output exit-code table. Kept here
// so the harness package doesn't reach into kit/output's internal
// codes when adopters use the harness in isolation.
//
// Source of truth: go/console/output/error.go. Refresh when the
// kit-side table grows.
var exitClassToCode = map[string]int{
	"OK":           0,
	"GENERIC":      1,
	"USAGE":        2,
	"NOT_FOUND":    3,
	"CONFLICT":     4,
	"UNAUTHORIZED": 5,
	"RATE_LIMITED": 64,
}

// ClassToExitCode resolves a kit exit-class symbol to its numeric
// code, defaulting to GENERIC (1) on unknown class names.
func ClassToExitCode(class string) int {
	if c, ok := exitClassToCode[strings.ToUpper(strings.TrimSpace(class))]; ok {
		return c
	}
	return 1
}

// exitCodeToClass returns a human-readable class name for an
// observed numeric exit code. Used in failure messages.
func exitCodeToClass(code int) string {
	for class, n := range exitClassToCode {
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
