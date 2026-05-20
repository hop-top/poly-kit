// Package cli provides a cobra+fang+viper root command factory for hop-top CLIs.
//
// # Factory
//
// [New] builds a fully-wired [Root] from a [Config] and optional functional
// options. Config fields control name, version, accent color, disabled
// flags, global flags, and help layout.
//
//	root := cli.New(cli.Config{
//	    Name:    "mytool",
//	    Version: "1.2.3",
//	    Short:   "A local-first CLI",
//	    Accent:  "#FF5F87",
//	})
//
// # Config
//
// Config drives what New registers:
//   - Name, Version, Short: identity and --version output
//   - Accent: lipgloss color used in Theme
//   - Disable: suppress built-in flags (Format, Quiet, NoColor, Hints)
//   - Globals: extra persistent flags registered on the root cobra.Command
//   - Help: section order, groups, disclaimer, alias display
//
// # Root
//
// Root is the returned value from New. Key fields:
//   - Cmd: the [cobra.Command] root
//   - Viper: bound [viper.Viper] instance
//   - Config: the resolved Config
//   - Theme: parity.Theme built from Accent
//   - Hints: output.Hints (respects --no-hints)
//   - Streams: output.Streams (stdout/stderr wrappers)
//   - Auth: JWT token from identity store (when WithIdentity used)
//   - Identity: *identity.Keypair (when WithIdentity used)
//   - Mesh: *peer.Mesh (when WithPeers used)
//
// Key accessors:
//   - [Root.InvokedAs]: caller-context signal read from the
//     KIT_INVOKED_AS environment variable at construction time.
//     Empty string means standalone invocation; a non-empty value
//     names the upstream tool (e.g. "tlc", "hop") that exec'd the
//     binary as a child. Env-var-only by design — there is no
//     --invoked-as flag.
//
// # Options
//
//   - [WithAPI]: scaffolds an HTTP API server (single listener) — adds
//     a "serve" subcommand wiring kit's canonical middleware stack
//     (RequestID → Logger → Recovery → JSON content-type → optional
//     Auth) on top of [api.NewRouter]; adds "token claims/decode"
//     subcommands when Auth is configured. For servers that need a
//     custom lifecycle, per-route auth carve-outs, or multiple
//     listeners, wire [api.NewRouter] directly instead — see
//     docs/specs/cli-multi-server.md.
//   - [WithIdentity]: loads or generates an Ed25519 keypair, signs a JWT
//   - [WithPeers]: starts mDNS discovery and trust mesh
//
// # Execution
//
// Execute wires context cancellation (SIGINT/SIGTERM), applies persistent
// pre-run logic (identity load, mesh start), and delegates to cobra:
//
//	root.Cmd.AddCommand(serveCmd(), syncCmd())
//	if err := root.Execute(context.Background()); err != nil {
//	    os.Exit(1)
//	}
package cli
