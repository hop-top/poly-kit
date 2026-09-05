package breaker_test

import (
	"fmt"
	"os"

	"hop.top/kit/go/console/cli/breaker"
	bpkg "hop.top/kit/go/core/breaker"
)

func ExampleCmd() {
	b := bpkg.New("demo-http")
	defer bpkg.Unregister("demo-http")
	b.Trip("upstream 503")
	fmt.Println("before:", b.State())

	cmd := breaker.Cmd()
	cmd.SetOut(os.Stdout)
	cmd.SetArgs([]string{"reset", "demo-http"})
	if err := cmd.Execute(); err != nil {
		fmt.Println("error:", err)
	}
	fmt.Println("after:", b.State())
	// Output:
	// before: open
	// reset breaker "demo-http"
	// after: closed
}
