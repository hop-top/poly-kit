package completion_test

import (
	"os"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/completion"
)

func ExampleBindArgs() {
	cmd := &cobra.Command{Use: "deploy <target>", Run: func(*cobra.Command, []string) {}}
	targets := completion.Prefixed("env", completion.StaticValues("prod", "preview", "staging"))
	completion.BindArgs(cmd, targets)

	// Drive cobra's hidden completion entry point the way a shell would.
	cmd.SetOut(os.Stdout)
	cmd.SetArgs([]string{cobra.ShellCompNoDescRequestCmd, "env:pr"})
	_ = cmd.Execute()
	// Output:
	// env:prod
	// env:preview
	// :4
}
