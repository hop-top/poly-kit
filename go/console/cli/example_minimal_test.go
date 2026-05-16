package cli_test

import (
	"fmt"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
)

// ExampleNew_minimal shows the smallest useful kit CLI: a one-file
// sidecar that opts out of the output suite (Disable.Format/Hints) and
// registers a single subcommand with RunE. No identity, no peers, no
// hint registry — just cobra wired through cli.New so the binary
// inherits hop.top conventions (--quiet, -V, -C, version template).
//
// The reader's takeaway: kit's overhead is optional, not mandatory.
func ExampleNew_minimal() {
	root := cli.New(cli.Config{
		Name:            "foo-scrape",
		Version:         "0.1.0",
		Short:           "Scrape one URL and print the result",
		Disable:         cli.Disable{Format: true, Hints: true},
		DisableValidate: true,
	})

	root.Cmd.AddCommand(&cobra.Command{
		Use:   "fetch [url]",
		Short: "Fetch a URL",
		RunE: func(_ *cobra.Command, args []string) error {
			fmt.Printf("fetched %s\n", args[0])
			return nil
		},
	})

	// In a real binary: root.Execute(context.Background()).
	fmt.Println(root.Config.Name)
	// Output:
	// foo-scrape
}
