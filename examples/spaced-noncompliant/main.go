// spaced-noncompliant is a deliberately-broken fixture binary used by
// the kit compliance test suite to validate F13 (ConsentingTelemetry)
// flag-accuracy. It reuses spaced's command tree so the binary
// genuinely responds to commands, but pairs with a toolspec that omits
// DO_NOT_TRACK from kill_switch_envs — exactly one sub-condition
// violation, no others.
//
// DO NOT use this binary as a copy/paste starting point. See the
// canonical example in hops/main/examples/spaced and the adopter
// reference at docs/adopters/reference/telemetry-compliance.md.
package main

import (
	"context"
	"os"

	"hop.top/kit/examples/spaced/go/cmd"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/bus"
)

func main() {
	root := cli.New(cli.Config{
		Name:             "spaced-noncompliant",
		Version:          "0.0.0-fixture",
		Short:            "deliberately non-compliant fixture for kit compliance F13 tests",
		MaxTopLevelVerbs: 12,
	})

	b := bus.New()

	// Wire just enough of spaced's command tree for compliance to walk
	// the consent surface. The toolspec declares the five canonical
	// consent subcommands; this command exposes them so the
	// b-sub-condition (subcommands resolve) still passes — only the
	// c-sub-condition (DO_NOT_TRACK kill-switch) is broken.
	root.Cmd.AddCommand(cmd.TelemetryCmd(root))
	root.Cmd.AddCommand(cmd.MissionCmd(root))
	root.Cmd.AddCommand(cmd.LaunchCmd(root, b))

	ctx := context.Background()
	if err := root.Execute(ctx); err != nil {
		_ = b.Close(ctx)
		os.Exit(1)
	}
	_ = b.Close(ctx)
}
