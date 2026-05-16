package config_test

// Tests in this file document API gaps in `hop.top/kit/go/core/config`
// that adopters keep reinventing. Same convention as
// go/console/cli/gaps_test.go: each test calls t.Skip with a "gap:"
// reason, and pins the current state so removing the skip yields a
// failing test until the gap is closed.
//
// See docs/audits/known-parity-gaps.md (or kit-api-gaps when split)
// for the gap rationale.

import (
	"testing"

	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

// Gap 3: no config.OptionsForTool("name") helper composing the
// canonical 4-layer cascade.
//
// Today, paths.go hard-codes pathsTool="kit", documents at line 32-34:
//
//	"tools that need a different name should compose their own
//	 Options and call Load directly"
//
// — and then every adopter does. The c12n review found several tools
// that drop the system layer entirely because the four-layer cascade
// (project → user → system → defaults) is not packaged as a single
// helper.
//
// Desired API:
//
//	opts := config.OptionsForTool("c12n")  // or "rsx", "tlc", ...
//	if err := config.Load(&cfg, opts); err != nil { ... }
//
// where Options is pre-populated with all four layers in the right
// order, parameterized on tool name. The sentinel "system" layer
// (/etc/<tool>/config.yaml) is the most-commonly-dropped layer in
// adopter reimplementations.
func TestGap_OptionsForTool_Missing(t *testing.T) {
	opts := config.OptionsForTool("c12n")

	// User and system paths must include the tool name (not "kit").
	require.Contains(t, opts.UserConfigPath, "c12n",
		"user path should be parameterized on tool name")
	require.NotContains(t, opts.UserConfigPath, "/kit/",
		"user path should not fall back to the package-default 'kit'")
	require.Contains(t, opts.SystemConfigPath, "c12n",
		"system path should be parameterized on tool name")
	require.NotContains(t, opts.SystemConfigPath, "/kit/",
		"system path should not fall back to the package-default 'kit'")

	// System layer must be present — this is the layer adopters drop
	// most often when rolling their own Options.
	require.NotEmpty(t, opts.SystemConfigPath)
	require.Equal(t, "/etc/c12n/config.yaml", opts.SystemConfigPath)
}

// pin: config.Options is the surface OptionsForTool would populate.
// If the helper ships, this pin keeps compiling — but the Skip above
// should be removed.
var _ = config.Options{}
