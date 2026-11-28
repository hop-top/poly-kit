// Command quickstart is the smallest kit CLI: one root from cli.New, one
// command annotated with its side effect, output through output.Dispatch.
package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// Greeting is the data the command returns. The json tags name the fields
// for --format json; the table tags name the columns for --format table.
type Greeting struct {
	Name    string `json:"name" table:"NAME"`
	Message string `json:"message" table:"MESSAGE"`
}

// version is stamped at build time: -ldflags "-X main.version=$TAG".
var version = "dev"

func newRoot() *cli.Root {
	root := cli.New(cli.Config{
		Name:    "greeter",
		Version: version,
		Short:   "Greets people",
	}, cli.WithStatus(cli.StatusConfig{}))

	greet := &cobra.Command{
		Use:   "greet <name>",
		Short: "Greet one person",
		Long:  "Greet one person by name and print the greeting as data.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			g := Greeting{Name: args[0], Message: "hello, " + args[0]}
			return output.Dispatch(cmd, root.Viper, g)
		},
	}
	// Every runnable leaf declares what it does to the world; Execute
	// refuses a tree whose leaves do not. The root also needs the reserved
	// status verb, here from cli.WithStatus.
	cli.SetSideEffect(greet, cli.SideEffectRead)
	cli.SetIdempotency(greet, cli.IdempotencyYes)
	cli.SetTopLevelVerb(greet) // a depth-1 leaf must say it is a verb on purpose

	root.Cmd.AddCommand(greet)
	return root
}

func main() {
	if err := newRoot().Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}
