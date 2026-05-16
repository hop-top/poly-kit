package harness

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli/conformance/harness/classifier"
	"hop.top/kit/go/console/cli/conformance/harness/diff"
	xrr "hop.top/xrr"
)

// AssertDestructiveGated runs cmd in three configurations and
// asserts the gating behavior:
//
//	(1) without --yes, without a TTY, no approval token →
//	    exits non-zero and records no mutating interactions.
//	(2) with --yes → proceeds and is allowed to record mutating
//	    interactions.
//	(3) with --yes and --dry-run together → soft-asserted to
//	    succeed with no mutation; a flag-conflict error is
//	    accepted with a warning.
//
// The leaf MUST be tagged kit/side-effect: destructive (any
// -local or -shared variant). If not, the harness fails with a
// programmer-error message via t.Fatalf.
func AssertDestructiveGated(t TB, cmd *cobra.Command, opts ...Option) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("AssertDestructiveGated: cmd is nil")
		return
	}
	c := apply(opts)
	leaf := resolveLeaf(cmd, c.args)
	if leaf == nil {
		t.Fatalf("AssertDestructiveGated: cannot resolve leaf for args %v", c.args)
		return
	}
	if !isDestructiveAnnotation(leaf.Annotations["kit/side-effect"]) {
		t.Fatalf(
			"AssertDestructiveGated: leaf %q is not destructive (side-effect=%q)\n\n"+
				"  AssertDestructiveGated is for leaves tagged kit/side-effect:\n"+
				"  destructive | destructive-local | destructive-shared.\n"+
				"  Either retag the leaf or use a different primitive.",
			leaf.CommandPath(),
			leaf.Annotations["kit/side-effect"],
		)
		return
	}

	base := c.cassetteDir
	if base == "" {
		base = t.TempDir()
	}

	// ── Case 1: no --yes, no TTY, no approval ─────────────────
	c1 := *c
	c1.cassetteDir = filepath.Join(base, "case-1")
	c1.mode = xrr.ModeRecord
	c1.withTTY = false
	res1 := runCaptured(&c1, cmd)
	mutating1 := mutatingInteractions(&c1, c.classifierOverrides())
	if res1.exitCode == 0 || len(mutating1) > 0 {
		var lines []string
		for _, m := range mutating1 {
			lines = append(lines, "    - "+m)
		}
		t.Errorf(
			"AssertDestructiveGated: case 1/3 — command should have refused\n\n"+
				"  invoked without --yes, no TTY, no approval token\n"+
				"  expected: non-zero exit + CONFIRMATION_REQUIRED-class envelope\n"+
				"  observed: exit %d, %d mutating interaction(s)\n%s\n\n"+
				"  cassette: %s",
			res1.exitCode, len(mutating1),
			strings.Join(lines, "\n"),
			c1.cassetteDir,
		)
		return
	}

	// ── Case 2: with --yes ────────────────────────────────────
	c2 := *c
	c2.args = appendUnique(c2.args, "--confirm=yes")
	c2.cassetteDir = filepath.Join(base, "case-2")
	c2.mode = xrr.ModeRecord
	res2 := runCaptured(&c2, cmd)
	if res2.runErr != nil || res2.exitCode != 0 {
		t.Errorf(
			"AssertDestructiveGated: case 2/3 — --yes should have permitted execution\n\n"+
				"  expected: exit 0 (OK)\n"+
				"  observed: exit %d, err=%v\n  stderr: %s",
			res2.exitCode, res2.runErr,
			truncate(res2.stderr.String(), 500),
		)
		return
	}

	// ── Case 3: --yes + --dry-run (soft assert) ───────────────
	c3 := *c
	c3.args = appendUnique(c3.args, "--confirm=yes")
	c3.args = appendUnique(c3.args, "--dry-run")
	c3.cassetteDir = filepath.Join(base, "case-3")
	c3.mode = xrr.ModeRecord
	res3 := runCaptured(&c3, cmd)
	if res3.exitCode != 0 {
		// Flag-conflict error accepted; emit a soft warning to t.Log.
		t.Logf(
			"AssertDestructiveGated: case 3/3 — --yes + --dry-run produced "+
				"exit %d (flag conflict). Accepted as a soft pass; kit policy "+
				"may flip this to no-op in a future release.",
			res3.exitCode,
		)
		return
	}
	mutating3 := mutatingInteractions(&c3, c.classifierOverrides())
	if len(mutating3) > 0 {
		var lines []string
		for _, m := range mutating3 {
			lines = append(lines, "    - "+m)
		}
		t.Errorf(
			"AssertDestructiveGated: case 3/3 — --dry-run should have suppressed mutations\n\n"+
				"  observed: exit 0, %d mutating interaction(s)\n%s\n\n  cassette: %s",
			len(mutating3),
			strings.Join(lines, "\n"),
			c3.cassetteDir,
		)
	}
}

func mutatingInteractions(c *config, ov classifier.Overrides) []string {
	interactions, err := diff.List(c.cassetteDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, it := range interactions {
		req := anyReqFromPayload(it.Adapter, it.ReqPayload)
		class := classifier.Classify(it.Adapter, req, ov)
		if class.IsMutating() {
			out = append(out, fmt.Sprintf("%s %s (classified: %s)",
				it.Adapter, it.Summary, class))
		}
	}
	return out
}

func isDestructiveAnnotation(v string) bool {
	switch v {
	case "destructive", "destructive-local", "destructive-shared":
		return true
	}
	return false
}

func appendUnique(args []string, v string) []string {
	if contains(args, v) {
		return args
	}
	return append(append([]string(nil), args...), v)
}
