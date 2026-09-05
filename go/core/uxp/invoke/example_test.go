package invoke_test

import (
	"fmt"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/adapters/claude"
)

// Build is pure: inspect Diagnostics before deciding to exec.
func ExampleInvocationAdapter() {
	spec, ds, err := claude.New().Build(invoke.Invocation{
		CLI:    uxp.CLIClaude,
		Mode:   invoke.ModeRun,
		Prompt: "summarize this repo",
	})
	if err != nil || ds.HasErrors() {
		fmt.Println("refused:", err, ds.Errors())
		return
	}
	fmt.Println(spec.Path, spec.Args)
	// Output: claude [-p summarize this repo]
}
