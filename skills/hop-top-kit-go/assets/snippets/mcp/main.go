// Command mcp is the quickstart tree served over MCP: the same cli.New
// root plus one option, mcpserve.With. `service start mcp --stdio` serves
// every command that may run remotely as an MCP tool.
package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/console/output"
)

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
	},
		cli.WithStatus(cli.StatusConfig{}),
		mcpserve.With(mcpserve.Config{}),
	)

	greet := &cobra.Command{
		Use:   "greet <name>",
		Short: "Greet one person",
		Long:  "Greet one person by name and print the greeting as data.",
		Args:  cobra.ExactArgs(1),
		// kit/args names the positional operand so a served surface can
		// carry it: MCP receives it as the "args" property.
		Annotations: map[string]string{"kit/args": "name"},
		RunE: func(cmd *cobra.Command, args []string) error {
			g := Greeting{Name: args[0], Message: "hello, " + args[0]}
			return output.Dispatch(cmd, root.Viper, g)
		},
	}
	cli.SetSideEffect(greet, cli.SideEffectRead)
	cli.SetIdempotency(greet, cli.IdempotencyYes)
	cli.SetTopLevelVerb(greet)

	root.Cmd.AddCommand(greet)
	return root
}

func main() {
	if err := newRoot().Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}
