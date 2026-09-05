package conformance_test

import (
	"fmt"

	"hop.top/kit/go/console/cli/conformance"
)

// ExampleExitCode shows how the conformance sentinels map to process
// exit codes.
func ExampleExitCode() {
	leak := conformance.LeakDetectedError("scenario-shaped block in README.md")
	code, known := conformance.ExitCode(leak)
	fmt.Println(code, known)

	cfg := conformance.ConfigError("bad allowlist", ".verifynoleak.allow:3", "remove the bare ignore")
	code, known = conformance.ExitCode(cfg)
	fmt.Println(code, known)

	code, known = conformance.ExitCode(fmt.Errorf("unrelated"))
	fmt.Println(code, known)
	// Output:
	// 66 true
	// 67 true
	// 0 false
}
