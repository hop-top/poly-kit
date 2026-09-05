// Stub binary for the `help <topic>` exit-code contract.
//
// Exit codes are the thing under test, and `go run` swallows them, so
// the sibling test builds this and invokes the built binary. The tree
// carries every shape the classification has to tell apart:
//
//   - init, a plain top-level command
//   - widget add, a nested command path
//   - config, a command in the hidden "management" group
//   - bonus, a command in the custom "extras" group
//   - extras, a command whose name collides with that group's ID, so
//     the command-wins tie-break is observable
//
// The exit code is read off the *output.Error envelope exactly as
// cmd/kit does, so what the test asserts is what a real adopter's
// binary would exit with.
package main

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

func main() {
	r := cli.New(cli.Config{
		Name:            "stubhelp",
		Version:         "0.1.0",
		Short:           "Help topic stub",
		Help:            cli.HelpConfig{Groups: []cli.GroupConfig{{ID: "extras", Title: "EXTRAS"}}},
		DisableValidate: true,
	})
	noop := func(*cobra.Command, []string) {}
	r.Cmd.AddCommand(&cobra.Command{Use: "init", Short: "Initialize things", Run: noop})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "config", Short: "Manage configuration", GroupID: "management", Run: noop})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "bonus", Short: "Bonus feature", GroupID: "extras", Run: noop})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "extras", Short: "Extras command shadowing the group ID", Run: noop})

	widget := &cobra.Command{Use: "widget", Short: "Widget things"}
	widget.AddCommand(&cobra.Command{Use: "add", Short: "Add a widget", Run: noop})
	r.Cmd.AddCommand(widget)

	if err := r.Execute(context.Background()); err != nil {
		var ce interface{ AsCLIError() *output.Error }
		if errors.As(err, &ce) {
			if e := ce.AsCLIError(); e != nil && e.ExitCode != 0 {
				os.Exit(e.ExitCode)
			}
		}
		os.Exit(1)
	}
}
