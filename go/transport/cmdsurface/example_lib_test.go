package cmdsurface_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/cmdsurface"
)

// ExampleInvokeArgs demonstrates the in-process Library surface:
// build a cobra tree, hand it to cmdsurface.New, and invoke a leaf
// by argv. InvokeArgs forces Meta.Surface = SurfaceLib and routes
// through the same Policy gate every other surface uses.
func ExampleInvokeArgs() {
	root := &cobra.Command{Use: "demo"}
	root.AddCommand(&cobra.Command{
		Use: "ping",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Print("pong")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	})

	b := cmdsurface.New(root)
	res, err := cmdsurface.InvokeArgs(context.Background(), b, []string{"ping"})
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(strings.TrimSpace(res.Stdout))
	// Output: pong
}

// ExampleInvokeArgs_flags shows how positional argv flags get
// surfaced into Invocation.Flags, and how WithFlag layers a
// programmatic override on top of the parsed form.
func ExampleInvokeArgs_flags() {
	root := &cobra.Command{Use: "demo"}
	widget := &cobra.Command{Use: "widget"}
	widget.AddCommand(&cobra.Command{
		Use: "add",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Print("added ", cmd.Flag("name").Value.String())
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "write"},
	})
	widget.Commands()[0].Flags().String("name", "", "name of the widget")
	root.AddCommand(widget)

	b := cmdsurface.New(root)
	res, err := cmdsurface.InvokeArgs(context.Background(), b,
		[]string{"widget", "add"},
		cmdsurface.WithFlag("name", "foo"),
	)
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(strings.TrimSpace(res.Stdout))
	// Output: added foo
}
