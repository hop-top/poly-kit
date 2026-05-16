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

// AssertDryRunNoMutation invokes cmd with --dry-run appended to
// args, records the run, and asserts the cassette contains zero
// non-Read interactions per the per-adapter mutation classifier.
//
// Non-Read is anything not classified ClassRead. Adopter overrides
// (WithExecClassifier, WithGRPCClassifier) feed into the
// classifier dispatch.
//
// Interactive leaves (kit/side-effect: interactive) reject
// --dry-run at the kit policy layer; AssertDryRunNoMutation on
// such a leaf surfaces a programmer-error message via t.Fatalf so
// the adopter knows to switch to a different primitive.
func AssertDryRunNoMutation(t TB, cmd *cobra.Command, opts ...Option) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("AssertDryRunNoMutation: cmd is nil")
		return
	}
	c := apply(opts)

	leaf := resolveLeaf(cmd, c.args)
	if leaf != nil && leaf.Annotations != nil {
		if leaf.Annotations["kit/side-effect"] == "interactive" {
			t.Fatalf(
				"AssertDryRunNoMutation: leaf %q is interactive\n\n"+
					"  --dry-run is rejected by the kit-level policy for interactive\n"+
					"  leaves. Use a different primitive (a future\n"+
					"  AssertInteractiveReject is being scoped) or remove the\n"+
					"  kit/side-effect=interactive annotation if it was misapplied.",
				leaf.CommandPath(),
			)
			return
		}
	}

	// Inject --dry-run if not already present.
	args := append([]string(nil), c.args...)
	if !contains(args, "--dry-run") {
		args = append(args, "--dry-run")
	}
	c.args = args

	// Default cassette dir under TempDir.
	if c.cassetteDir == "" {
		c.cassetteDir = filepath.Join(t.TempDir(), "cassettes")
	}
	if c.mode == "" {
		c.mode = xrr.ModeRecord
	}

	res := runCaptured(c, cmd)
	if res.runErr != nil {
		t.Errorf(
			"AssertDryRunNoMutation: --dry-run invocation failed: %v\n  stderr: %s",
			res.runErr, truncate(res.stderr.String(), 500),
		)
		return
	}

	interactions, err := diff.List(c.cassetteDir)
	if err != nil {
		t.Errorf("AssertDryRunNoMutation: list cassettes: %v", err)
		return
	}

	ov := c.classifierOverrides()
	var violations []string
	for _, it := range interactions {
		req := anyReqFromPayload(it.Adapter, it.ReqPayload)
		class := classifier.Classify(it.Adapter, req, ov)
		if class == classifier.ClassRead {
			continue
		}
		violations = append(violations,
			fmt.Sprintf("  ✗ %-5s %s   (classified: %s)",
				it.Adapter, it.Summary, class))
	}
	if len(violations) == 0 {
		return
	}
	t.Errorf(
		"AssertDryRunNoMutation: %d mutating interaction(s) under --dry-run\n\n%s\n\ncassette: %s",
		len(violations),
		strings.Join(violations, "\n"),
		c.cassetteDir,
	)
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
