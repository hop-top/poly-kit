package harness

import (
	"io"

	"hop.top/kit/go/console/cli"
)

// The harness drives interactive prompts through kit's exported
// prompt seam rather than through the command's stdin.
//
// cmd.SetIn feeds the payload. A prompt reads the controlling
// terminal, which no test process has, so without this the harness
// could only ever observe the refusal path — and an adopter wanting
// to exercise "prompted, answered yes, payload intact" had to patch
// kit. cli.WithPromptSource is that seam; this installs it globally
// for one invocation because the harness holds a *cobra.Command, not
// the *cli.Root the option applies to.
func init() {
	installPromptSourceFn = func(r io.Reader, w io.Writer) func() {
		return cli.InstallGlobalPromptSource(
			cli.PromptSourceFromReadWriter(r, w))
	}
}
