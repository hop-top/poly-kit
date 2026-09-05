package svc_test

import (
	"errors"
	"fmt"

	"hop.top/kit/go/console/cli/conformance/svc"
	"hop.top/kit/go/console/output"
)

// ExampleCmd mounts the svc tree and shows serve refusing to start
// without a scenario root.
func ExampleCmd() {
	cmd := svc.Cmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"serve", "--claims-db", "/tmp/claims.sqlite"})

	err := cmd.Execute()
	var cliErr *output.Error
	if errors.As(err, &cliErr) {
		fmt.Println(cliErr.Code, cliErr.ExitCode)
		fmt.Println(cliErr.Message)
	}
	// Output:
	// USAGE 2
	// --scenarios-root is required (or KIT_CONF_SVC_SCENARIOS_ROOT)
}
