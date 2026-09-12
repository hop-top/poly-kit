package policy_test

import (
	"os/exec"
	"strings"
	"testing"
)

// forbiddenDepPrefixes are the module paths of the terminal-UI stack.
// charmbracelet ships the ANSI/TTY primitives; charm.land is the
// lipgloss v2 import path used by the styled renderers.
var forbiddenDepPrefixes = []string{
	"github.com/charmbracelet/",
	"charm.land/",
}

// runtime/policy is library code. Adopters embed it in trees that
// deliberately keep a library/UI boundary and must not link a terminal
// renderer, so its transitive dependency set has to stay free of the
// styling stack.
//
// This regressed once already: AsCLIError was implemented against
// hop.top/kit/go/console/output, and because Go's unit of dependency is
// the package — not the file or the symbol — that single import pulled
// the whole of console/output, lipgloss and all, into every consumer of
// this package. The envelope now lives in the console/output/envelope
// leaf and the edge is gone.
//
// Guarding the transitive set rather than the import list is the point:
// a direct import of console/output is easy to spot in review, but the
// same breakage arrives silently through any new intermediate package.
//
// Not gated on testing.Short: `make test-go` runs -short, and a guard
// that does not run in the everyday gate is not a guard.
func TestPolicyDoesNotDependOnTerminalUIStack(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	var found []string
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		for _, prefix := range forbiddenDepPrefixes {
			if strings.HasPrefix(dep, prefix) {
				found = append(found, dep)
				break
			}
		}
	}

	if len(found) > 0 {
		t.Errorf("runtime/policy must not depend on the terminal-UI stack, "+
			"but %d such package(s) are in its transitive dependency set:\n  %s\n\n"+
			"A package-level import is the usual cause: importing any package "+
			"that links lipgloss links it here too, even when only one "+
			"lipgloss-free symbol is used. Build error envelopes from "+
			"hop.top/kit/go/console/output/envelope (a leaf), not from "+
			"hop.top/kit/go/console/output (which carries the renderers).",
			len(found), strings.Join(found, "\n  "))
	}
}
