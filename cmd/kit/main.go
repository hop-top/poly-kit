package main

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"
	kitinit "hop.top/kit/cmd/kit/init"
	kittmpl "hop.top/kit/cmd/kit/template"
	"hop.top/kit/go/console/cli"
	breakercmd "hop.top/kit/go/console/cli/breaker"
	configcmd "hop.top/kit/go/console/cli/config"
	conformancecmd "hop.top/kit/go/console/cli/conformance"
	scopecmd "hop.top/kit/go/console/cli/scope"
	"hop.top/kit/go/console/output"
	coreconfig "hop.top/kit/go/core/config"
	uxpcmd "hop.top/kit/go/core/uxp/invoke/cmd/uxp"
)

var version = "dev"

// commandGroups maps each top-level command name to its cobra GroupID per
// the §4.1 taxonomy from cli-conventions-with-kit.md. Every visible
// top-level command should have an entry; unmapped commands fall back to
// the default "COMMANDS" group.
var commandGroups = map[string]string{
	// ORGANIZE — scaffolding, project layout, templates.
	"init":     "organize",
	"template": "organize",
	"symlink":  "organize",

	// INTERACT — long-running interactive surfaces.
	"serve": "interact",

	// INSTANCE — node-bound concerns (mesh peers, audit).
	"peer": "instance",

	// MANAGEMENT — diagnostics, configuration, introspection.
	"config":      "management",
	"breaker":     "management",
	"conformance": "management",
	"scope":       "management",
	"toolspec":    "management",
	"uxp":         "management",
}

// applyCommandGroups walks the root's children and assigns GroupID from
// commandGroups. Must run after all subcommands are registered so every
// child sees its assignment.
func applyCommandGroups(root *cli.Root) {
	for _, c := range root.Cmd.Commands() {
		if id, ok := commandGroups[c.Name()]; ok {
			c.GroupID = id
		}
	}
}

func main() {
	root := cli.New(cli.Config{
		Name:    "kit",
		Version: version,
		Short:   "Generic document engine for kit apps",
		// Help.Groups registers custom groups in display order. Kit adds
		// the built-in "management" group (Hidden) automatically — do not
		// re-list it here or AddGroup would duplicate it. The §4.1
		// taxonomy from cli-conventions-with-kit.md drives the ordering.
		Help: cli.HelpConfig{
			Groups: []cli.GroupConfig{
				{ID: "organize", Title: "ORGANIZE"},
				{ID: "interact", Title: "INTERACT"},
				{ID: "instance", Title: "INSTANCE"},
			},
		},
	},
		cli.WithIdentity(cli.IdentityConfig{}),
		cli.WithPeers(cli.PeerConfig{}),
		// Mount the reserved `kit status` subcommand so the kit
		// binary itself dogfoods the the static-conformance contract surface. Six
		// default providers (profile/env/workspace/auth/effective-
		// config/kit-annotations) cover the introspection contract;
		// adopters who fork kit add their own via
		// root.RegisterStatusProvider before Execute.
		cli.WithStatus(cli.StatusConfig{}),
	)
	root.Cmd.AddCommand(serveCmd(root))
	root.Cmd.AddCommand(kitinit.InitCmd(root))
	root.Cmd.AddCommand(kittmpl.GroupCmd(root))
	root.Cmd.AddCommand(symlinkCmd(root))
	root.Cmd.AddCommand(scopecmd.Cmd())
	root.Cmd.AddCommand(breakercmd.Cmd())
	root.Cmd.AddCommand(conformancecmd.Cmd())
	root.Cmd.AddCommand(configCmd())
	root.Cmd.AddCommand(toolspecCmd(root))
	root.Cmd.AddCommand(uxpcmd.Cmd())

	applyCommandGroups(root)

	if err := root.Execute(context.Background()); err != nil {
		// The kit RunE middleware wraps every leaf error into an
		// *output.Error envelope carrying the authoritative ExitCode.
		// Honor it: typed AsCLIError errors (envelopes, conformance
		// sentinels, output.UsageError/NotFoundError/etc.) all set
		// ExitCode deliberately, and previously had it collapsed to 1
		// by this switch. Now it survives.
		if code := exitCodeFromEnvelope(err); code != 0 {
			os.Exit(code)
		}
		// Context cancellation is the one signal not modeled as an
		// AsCLIError envelope (it comes from the cobra/fang plumbing).
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		os.Exit(1)
	}
}

// exitCodeFromEnvelope returns the ExitCode carried by an *output.Error
// envelope (set by RunE middleware on every wrapped leaf), or 0 if err
// doesn't unwrap to one.
func exitCodeFromEnvelope(err error) int {
	type asCLIError interface{ AsCLIError() *output.Error }
	var ce asCLIError
	if errors.As(err, &ce) {
		if out := ce.AsCLIError(); out != nil && out.ExitCode != 0 {
			return out.ExitCode
		}
	}
	return 0
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect kit configuration",
		Args:  cobra.NoArgs,
	}
	configcmd.RegisterPathSubcommands(cmd, "kit", configcmd.WithResolver(kitResolver))
	return cmd
}

func kitResolver(cwd string) []configcmd.ResolvedPath {
	src := coreconfig.Paths(cwd)
	out := make([]configcmd.ResolvedPath, len(src))
	for i, p := range src {
		out[i] = configcmd.ResolvedPath{
			Path:   p.Path,
			Source: p.Source,
			Scope:  p.Scope,
			Exists: p.Exists,
		}
	}
	return out
}
