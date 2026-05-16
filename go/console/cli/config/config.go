// Package config provides shared "config path" / "config paths" cobra
// subcommands that any kit-built CLI can attach to its own `config`
// parent command.
//
// Convention parity with `git config --list --show-origin`,
// `npm config get`, `gh config get`, `kubectl config view`. Every kit
// CLI exposes:
//
//	<tool> config path     # highest-precedence existing config file (one line)
//	<tool> config paths    # full ordered chain, highest-precedence first
//
// Both subcommands honor --format=text|json|yaml (default text) and
// --from <dir> for an explicit cwd override.
//
// Adoption is one line on the host's existing `config` parent:
//
//	cfgCmd := &cobra.Command{Use: "config", Short: "Inspect kit configuration"}
//	kitcliconfig.RegisterPathSubcommands(cfgCmd, "kit")
//	rootCmd.AddCommand(cfgCmd)
//
// The resolver -- the function that walks the precedence chain and
// returns []ResolvedPath -- is supplied by the host via WithResolver.
// The default resolver returns nil (so `config path` exits 1 with a
// "no config file found" message and `config paths` prints nothing).
// Hosts wire their own resolver using core/config.Paths once available.
package config

import (
	"github.com/spf13/cobra"
)

// ResolvedPath describes one rung of the config precedence chain.
// Field ordering and JSON/YAML tag names match the core/config.ResolvedPath
// contract (Agent A) so the two types are wire-compatible: callers can
// pass core/config values straight through via a small adapter.
type ResolvedPath struct {
	Path   string `json:"path" yaml:"path"`
	Source string `json:"source" yaml:"source"`
	Scope  string `json:"scope" yaml:"scope"`
	Exists bool   `json:"exists" yaml:"exists"`
}

// Resolver returns the ordered config precedence chain for cwd,
// highest-precedence first. Implementations should never return nil
// entries; an empty slice means "no config sources discovered".
type Resolver func(cwd string) []ResolvedPath

// Option mutates the shared command configuration.
type Option func(*options)

type options struct {
	resolver Resolver
}

// WithResolver overrides the default (no-op) resolver. Hosts pass an
// adapter around core/config.Paths once Agent A's API is available:
//
//	kitcliconfig.WithResolver(func(cwd string) []kitcliconfig.ResolvedPath {
//	    raw := coreconfig.Paths(cwd)
//	    out := make([]kitcliconfig.ResolvedPath, len(raw))
//	    for i, r := range raw {
//	        out[i] = kitcliconfig.ResolvedPath(r)
//	    }
//	    return out
//	})
func WithResolver(r Resolver) Option {
	return func(o *options) { o.resolver = r }
}

func newOptions(opts ...Option) *options {
	o := &options{resolver: defaultResolver}
	for _, fn := range opts {
		fn(o)
	}
	if o.resolver == nil {
		o.resolver = defaultResolver
	}
	return o
}

// defaultResolver returns nil so unwired hosts produce a clear "no
// config file found" message instead of panicking.
func defaultResolver(_ string) []ResolvedPath { return nil }

// Command returns a parent `config` command with `path` and `paths`
// subcommands attached. Hosts that already have a `config` parent
// (e.g. tlc) should call RegisterPathSubcommands on that parent
// instead of attaching the whole subtree returned here.
func Command(toolName string, opts ...Option) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect " + toolName + " configuration",
		Args:  cobra.NoArgs,
	}
	RegisterPathSubcommands(cmd, toolName, opts...)
	return cmd
}

// RegisterPathSubcommands attaches `path` and `paths` to parent. Use
// this when the host already owns the `config` parent command and
// just wants to opt in to the shared introspection subcommands.
func RegisterPathSubcommands(parent *cobra.Command, toolName string, opts ...Option) {
	o := newOptions(opts...)
	parent.AddCommand(pathCommand(toolName, o))
	parent.AddCommand(pathsCommand(toolName, o))
}

// PathCommand returns a standalone `path` subcommand for hosts that
// prefer to attach subcommands one at a time.
func PathCommand(toolName string, opts ...Option) *cobra.Command {
	return pathCommand(toolName, newOptions(opts...))
}

// PathsCommand returns a standalone `paths` subcommand.
func PathsCommand(toolName string, opts ...Option) *cobra.Command {
	return pathsCommand(toolName, newOptions(opts...))
}
