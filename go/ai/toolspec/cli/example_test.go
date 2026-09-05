package cli_test

import (
	"fmt"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/toolspec/cli"
	kitcli "hop.top/kit/go/console/cli"
)

func Example() {
	root := kitcli.New(kitcli.Config{Name: "demo", Version: "1.0.0", Short: "Demo tool"})
	list := &cobra.Command{Use: "list", Short: "List items", RunE: func(*cobra.Command, []string) error { return nil }}
	kitcli.SetSideEffect(list, kitcli.SideEffectRead)
	kitcli.SetIdempotency(list, kitcli.IdempotencyYes)
	root.Cmd.AddCommand(list)

	m := cli.EmitManifest(root, "1.0")
	fmt.Println(m.Tool, m.SchemaVersion, len(m.Commands))
	fmt.Println(m.Commands[0].Path, m.Commands[0].SideEffect, m.Commands[0].Idempotent)
	// Output:
	// demo 1.0 1
	// [demo list] read yes
}
