package grade_test

import (
	"errors"
	"fmt"

	"hop.top/kit/go/console/cli/conformance/grade"
	"hop.top/kit/go/console/output"
)

// ExampleCmd shows the leaf rejecting an out-of-range tier before any
// network call; the error is a structured *output.Error.
func ExampleCmd() {
	cmd := grade.Cmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"./cassette", "--tier", "9"})

	err := cmd.Execute()
	var cliErr *output.Error
	if errors.As(err, &cliErr) {
		fmt.Println(cliErr.Code, cliErr.ExitCode)
		fmt.Println(cliErr.Message)
	}
	// Output:
	// USAGE 2
	// conformance grade: --tier must be 1/2/3, got 9
}
