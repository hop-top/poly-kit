package record_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/conformance"
)

// TestStrictGates_HarnessSubtree mounts the conformance tree under a
// strict kit root (EnforceValidate on, as cli.New defaults) and locks
// in the harness subtree's validation profile:
//
//   - Layer-A (annotations, Short/Long, shape): fully clean with NO
//     exemption — the record leaf carries the complete contract
//     surface and the harness group + conformance parent carry
//     kit/hierarchical for the depth-3 rule.
//   - Signature validator: the only finding is the local-globals
//     shadow produced by the shared output flag registration — the
//     exact profile the grade leaf carries. Any new violation class
//     (reserved-name, depth-hierarchical, passthrough) fails here.
func TestStrictGates_HarnessSubtree(t *testing.T) {
	root := cli.New(cli.Config{
		Name:    "kit",
		Version: "test",
		Short:   "strict-gate fixture root",
	}, cli.WithStatus(cli.StatusConfig{}))
	root.Cmd.AddCommand(conformance.Cmd())

	// Layer-A: the harness subtree must not appear in any issue.
	if err := root.Validate(); err != nil {
		assert.NotContains(t, err.Error(), "harness",
			"harness subtree must pass Layer-A validation without exemption")
	}

	// Signature: pin the profile to grade-parity. The record leaf and
	// the grade leaf both register the shared output flag set on the
	// leaf, which the local-globals check reports when mounted under
	// a root owning the same globals; that shared pattern is tracked
	// family-wide and is the only accepted finding.
	report := root.ValidateSignature()
	require.NotNil(t, report)
	var gradeLocalGlobals, recordLocalGlobals string
	for _, v := range report.Violations {
		switch v.Path {
		case "kit conformance grade":
			if v.Check == cli.SignatureCheckLocalGlobals {
				gradeLocalGlobals = v.Detail
			}
		case "kit conformance harness record":
			require.Equal(t, cli.SignatureCheckLocalGlobals, v.Check,
				"unexpected signature violation class on the record leaf: [%s] %s", v.Check, v.Detail)
			recordLocalGlobals = v.Detail
		default:
			assert.NotContains(t, v.Path, "harness",
				"unexpected signature violation in harness subtree: [%s] %s", v.Check, v.Detail)
		}
	}
	assert.Equal(t, gradeLocalGlobals, recordLocalGlobals,
		"record leaf must shadow exactly the same shared output flags as the grade leaf, nothing more")
}
