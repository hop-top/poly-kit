package record_test

import (
	"errors"
	"fmt"

	"hop.top/kit/go/console/cli/conformance/harness/record"
	"hop.top/kit/go/console/output"
)

// ExampleGroup mounts the harness group and shows the record leaf
// refusing to run without its required flags.
func ExampleGroup() {
	harness := record.Group()
	harness.SilenceUsage = true
	harness.SilenceErrors = true
	harness.SetArgs([]string{"record", "--binary", "./bin/acme", "--out", "./cassette"})

	err := harness.Execute()
	var cliErr *output.Error
	if errors.As(err, &cliErr) {
		fmt.Println(cliErr.Code, cliErr.ExitCode)
		fmt.Println(cliErr.Message)
	}
	// Output:
	// USAGE 3
	// conformance harness record: --scenario is required
}
