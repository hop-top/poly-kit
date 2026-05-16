// Package breaker provides the "kit breaker" CLI subcommand tree
// for inspecting runtime circuit breakers: list, show, reset.
//
// Subcommands:
//
//	kit breaker list  [--format json|table|yaml]
//	kit breaker show  <name> [--format ...]
//	kit breaker reset <name> | --all [--yes]
//
// All commands honor --format via go/console/output. Exit codes:
// 0 ok, 1 not-found, 2 usage error.
//
// Limitation: list/show/reset only see breakers in THIS process.
// Cross-process introspection needs IPC and is out of scope.
package breaker

import (
	"fmt"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/output"
	bpkg "hop.top/kit/go/core/breaker"
)

// Cmd returns the top-level "breaker" command with all subcommands
// attached.
func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "breaker",
		Short: "Inspect runtime circuit breakers",
		Long: `Inspect kit/breaker runtime fuses: list registered
breakers, show one in detail, or reset by name.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(listCmd(), showCmd(), resetCmd())
	return cmd
}

// notFound is returned when a name doesn't resolve in the registry.
// Renders as a CodeGeneric envelope with exit code 1 to preserve the
// historical contract (breaker package doc, header §"Exit codes":
// "1 not-found"). Kit-wide convention would map not-found to exit 3
// via output.NotFoundError; preserving 1 here is deliberate.
type notFound struct{ name string }

func (n notFound) Error() string { return fmt.Sprintf("breaker not found: %s", n.name) }

// AsCLIError makes the historical exit-code-1 contract survive kit's
// RunE error-envelope wrapping.
func (n notFound) AsCLIError() *output.Error {
	return &output.Error{Code: output.CodeGeneric, Message: n.Error(), ExitCode: 1}
}

// IsNotFound reports whether err is the sentinel returned for
// missing-by-name lookups, so root callers can map to exit code 1.
func IsNotFound(err error) bool {
	_, ok := err.(notFound)
	return ok
}

// lookupOrError returns the named breaker or notFound{name}.
func lookupOrError(name string) (bpkg.Breaker, error) {
	b, ok := bpkg.Lookup(name)
	if !ok {
		return nil, notFound{name: name}
	}
	return b, nil
}
