package cmdmeta_test

import (
	"fmt"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli/cmdmeta"
)

func ExampleGetNextSteps() {
	cmd := &cobra.Command{
		Use: "deploy",
		Annotations: map[string]string{
			cmdmeta.KeySideEffect: "write",
			cmdmeta.KeyRetryable:  "true",
			cmdmeta.KeyNextSteps:  `[{"when":"on success","suggest":"kit status","reason":"confirm rollout"}]`,
		},
	}

	fmt.Println("retryable:", cmdmeta.IsRetryable(cmd))
	fmt.Println("dry-run:", cmdmeta.IsDryRunSupported(cmd))
	steps, ok := cmdmeta.GetNextSteps(cmd)
	fmt.Println(ok, steps[0].Suggest, "/", steps[0].Reason)
	// Output:
	// retryable: true
	// dry-run: true
	// true kit status / confirm rollout
}
