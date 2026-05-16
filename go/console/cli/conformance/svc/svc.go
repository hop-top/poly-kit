// Package svc wires the "kit conformance svc {serve,token …}" cobra
// subcommand tree. It binds the go/conformance/svc service to operator
// flags + the kit binary entry point.
package svc

import (
	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
)

// Cmd returns the "svc" subcommand tree.
func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "svc",
		Short: "Run the conformance grading service",
		Long: `Operator surface for the conformance grading service.

Subcommands:
  serve          Start the HTTP grading service.
  token mint     Mint a new bearer token (prints plaintext ONCE).
  token list     List existing claims (token_sha256 + scopes; never plaintext).
  token revoke   Revoke a claim by token_id.

The service lives at hop.top/kit/go/conformance/svc; see design at
.tlc/tracks/svc/design.md.`,
		Args: cobra.NoArgs,
	}
	serve := serveCmd()
	tokenGroup := tokenCmd()
	for _, c := range []*cobra.Command{serve, tokenGroup} {
		// These leaves manage long-running services or operator-side
		// state. Exempt from Layer-A validation because they predate
		// the annotation surface (mirrors verifynoleak pattern).
		cli.SetExemptValidation(c)
	}
	cmd.AddCommand(serve, tokenGroup)
	return cmd
}
