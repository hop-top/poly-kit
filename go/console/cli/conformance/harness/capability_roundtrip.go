package harness

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// AssertCapabilityRoundtrip discovers the leaf catalog from
// cmd by walking its subcommand tree, and re-invokes each
// non-interactive leaf with --help. Each re-invocation must exit OK.
//
// Interactive leaves (kit/side-effect: interactive) are skipped —
// re-invoking them with --help is safe but the walker filter
// keeps the assertion focused on the contract.
//
// Failures collect across all leaves; the test reports the whole
// set at once. WithFailFast() flips to stop-on-first-failure.
//
// Re-invocations run sequentially against the shared root cobra
// command, which is not safe for concurrent Execute calls. The
// WithParallelism option is therefore advisory; future work to
// rebuild the root per invocation would lift the constraint.
func AssertCapabilityRoundtrip(t TB, cmd *cobra.Command, opts ...Option) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("AssertCapabilityRoundtrip: cmd is nil")
		return
	}
	c := apply(opts)

	leaves := discoverLeaves(cmd)
	if len(leaves) == 0 {
		t.Fatalf(
			"AssertCapabilityRoundtrip: leaf catalog not discoverable\n\n" +
				"  walked cmd tree found zero runnable non-interactive leaves.\n" +
				"  ensure your root cobra command has registered subcommands and\n" +
				"  that each runnable leaf carries kit/side-effect annotations.",
		)
		return
	}

	var (
		failures []string
		skipped  []string
	)
	for _, leaf := range leaves {
		if leaf.Annotations != nil && leaf.Annotations["kit/side-effect"] == "interactive" {
			skipped = append(skipped, leaf.CommandPath())
			continue
		}
		path := trimCommandPath(cmd, leaf)
		res := invokeHelp(cmd, c, path)
		if res.exitCode == 0 && res.runErr == nil {
			continue
		}
		if c.leafExitOverride != nil {
			if expected, ok := c.leafExitOverride[path]; ok {
				if exitMatches(res.exitCode, []string{expected}) {
					continue
				}
			}
		}
		failures = append(failures, fmt.Sprintf(
			"  ✗ %s\n      exit: %d (%s)\n      stderr: %s",
			path, res.exitCode, exitCodeToClass(res.exitCode),
			truncate(strings.TrimSpace(res.stderr.String()), 200),
		))
		if c.failFast {
			break
		}
	}

	for _, s := range skipped {
		t.Logf("AssertCapabilityRoundtrip: skipped interactive leaf %s", s)
	}
	if len(failures) == 0 {
		return
	}
	t.Errorf(
		"AssertCapabilityRoundtrip: %d of %d leaves failed --help re-invocation\n\n%s",
		len(failures), len(leaves)-len(skipped),
		strings.Join(failures, "\n\n"),
	)
}

// discoverLeaves walks cmd and returns every runnable leaf (sans
// cobra built-ins). The capability-discovery path could route
// through `spec --format json` (the convention), but walking the
// in-process tree is faster and equivalent for tests.
func discoverLeaves(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	walkLeaves(root, func(c *cobra.Command) { out = append(out, c) })
	return out
}

// invokeHelp runs `<root> <path> --help` against a freshly created
// shadow argv. cobra's Help printout returns nil error and exit 0
// when the command is well-formed.
func invokeHelp(root *cobra.Command, c *config, leafPath string) *runResult {
	args := append(strings.Fields(leafPath), "--help")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetIn(bytes.NewReader(nil))
	runErr := root.Execute()
	return &runResult{
		stdout: stdout,
		stderr: stderr,
		runErr: runErr,
		exitCode: func() int {
			if runErr != nil {
				return exitCodeFromError(runErr)
			}
			return 0
		}(),
	}
}
