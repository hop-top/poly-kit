// spaced is a satirical SpaceX CLI historian and parity test vehicle for hop.top/kit/cli.
//
// It exercises the full kit CLI contract: global flags, format flag, help output,
// version, comma-list flags, short flags, subcommand trees, and structured output.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/examples/spaced/go/cmd"
	"hop.top/kit/go/console/alias"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/runtime/bus"
	"hop.top/uri/handle/generate"
	"hop.top/uri/scheme"
)

const disclaimer = `Not affiliated with, endorsed by, or in any way authorized by SpaceX,
Elon Musk, DOGE, NASA, the FAA, or the Starman mannequin currently past Mars.
We would, however, accept a sponsorship (https://github.com/sponsors/hop-top).
Cash, Starlink credits, or a ride on the next Crew Dragon all acceptable.`

func main() {
	root := cli.New(cli.Config{
		Name:             "spaced",
		Version:          "0.1.0",
		Short:            "satirical SpaceX CLI historian — every launch, every RUD, every daemon",
		Accent:           "#FF5733",
		Help:             cli.HelpConfig{Disclaimer: disclaimer},
		MaxTopLevelVerbs: 12,
		// Compose into kit's PersistentPreRunE chain — direct
		// assignment to r.Cmd.PersistentPreRunE would silently
		// overwrite the built-in chdir → identity → peer chain. The
		// hook parses --telemetry and stamps a start time on ctx so
		// PersistentPostRunE can compute duration_ms.
		Hooks: cli.Hooks{PrePersistentRunE: installTelemetryPreRunHook},
	}, cli.WithStatus(cli.StatusConfig{ExtraEnvKeys: []string{"SPACED_*"}}), cli.WithURI(spacedURIConfig()))

	// --telemetry={off,anon,full} persistent flag. Parsed by
	// installTelemetryPreRunHook and resolved per-invocation via
	// telemetry.WithMode (precedence #1, beats env vars and SetMode).
	//
	// Visible in --help; spaced py + ts mirror this flag with the same
	// shape so the cross-lang parity contract includes it.
	root.Cmd.PersistentFlags().String("telemetry", "off", "kit-telemetry emit mode (off|anon|full)")

	b := bus.New()

	// Initialize kit-telemetry against the shared bus. Must run after
	// bus.New so the emitter publishes onto the same bus the demo's
	// other subscribers observe.
	initTelemetry(b)

	// Emit one telemetry.event.recorded at command exit. RunE errors
	// don't reach here — root.Execute swallows them and returns to
	// main; full exit-code capture is a follow-up (see ADR-0035).
	root.Cmd.PersistentPostRunE = installTelemetryPostRun

	// Log launch and daemon events.
	b.SubscribeAsync("kit.spaced.launch.#", func(_ context.Context, e bus.Event) {
		fmt.Printf("  [bus] %s → %v\n", e.Topic, e.Payload)
	})
	b.SubscribeAsync("kit.spaced.daemon.#", func(_ context.Context, e bus.Event) {
		fmt.Printf("  [bus] %s → %v\n", e.Topic, e.Payload)
	})

	// User-facing commands (default COMMANDS group).
	root.Cmd.AddCommand(cmd.MissionCmd(root))
	root.Cmd.AddCommand(cmd.LaunchCmd(root, b))
	root.Cmd.AddCommand(cmd.AbortCmd(root))
	root.Cmd.AddCommand(cmd.TelemetryCmd(root))
	root.Cmd.AddCommand(cmd.CountdownCmd(root))
	root.Cmd.AddCommand(cmd.FleetCmd(root))
	root.Cmd.AddCommand(cmd.StarshipCmd(root))
	root.Cmd.AddCommand(cmd.ElonCmd(root))
	root.Cmd.AddCommand(cmd.IpoCmd(root))
	root.Cmd.AddCommand(cmd.CompetitorCmd(root))
	root.Cmd.AddCommand(cmd.DaemonCmd(root, b))
	root.Cmd.AddCommand(cmd.ServeCmd())
	root.Cmd.AddCommand(cmd.SyncCmd())
	root.Cmd.AddCommand(cmd.PeerCmd())

	// Alias store at XDG config path.
	cfgDir := xdg.MustEnsure(xdg.ConfigDir("spaced"))
	store := alias.NewStore(filepath.Join(cfgDir, "aliases.yaml"))
	_ = store.Load()

	aliasCmd := root.AliasCmd(store)
	aliasCmd.GroupID = "management"
	root.Cmd.AddCommand(aliasCmd)

	// Load persisted aliases into the command tree.
	if err := root.LoadAliasStore(store); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}

	// Management commands (hidden by default, shown with --help-all).
	authCmd := cmd.AuthStatusCmd(root)
	authCmd.GroupID = "management"
	root.Cmd.AddCommand(authCmd)

	configCmd := cmd.ConfigShowCmd()
	configCmd.GroupID = "management"
	root.Cmd.AddCommand(configCmd)

	toolspecCmd := cmd.ToolspecCmd()
	toolspecCmd.GroupID = "management"
	root.Cmd.AddCommand(toolspecCmd)

	complianceCmd := cmd.ComplianceCmd()
	complianceCmd.GroupID = "management"
	root.Cmd.AddCommand(complianceCmd)

	normalizeSpacedCLI(root.Cmd)

	ctx := context.Background()
	if err := root.Execute(ctx); err != nil {
		_ = b.Close(ctx)
		os.Exit(1)
	}
	_ = b.Close(ctx)
}

func spacedURIConfig() cli.URIConfig {
	return cli.URIConfig{
		Policy: scheme.Policy{
			DefaultNamespaceSegments: 2,
			SchemeNamespaceSegments:  map[string]int{"spaced": 2},
			VanityAliases: []scheme.VanityAlias{
				{From: "spaced://ift-5", To: "spaced://hop-top/spaced/IFT-5"},
				{From: "spaced://starship", To: "spaced://hop-top/spaced/IFT-5"},
				{From: "spaced://starman", To: "spaced://hop-top/spaced/Starman"},
			},
			ActionRoutes: map[string]scheme.ActionRoute{
				"mission.inspect": {
					Command: "spaced",
					Args:    []string{"mission", "inspect", "{id}"},
				},
			},
		},
		Types: []scheme.TypeRegistration{{
			Name: "spaced",
			Completer: func(_ context.Context, prefix string) ([]string, error) {
				values := []string{"hop-top/spaced/IFT-5", "hop-top/spaced/IFT-6", "hop-top/spaced/Starman"}
				if prefix == "" {
					return values, nil
				}
				out := values[:0]
				for _, value := range values {
					if strings.Contains(strings.ToLower(value), strings.ToLower(prefix)) {
						out = append(out, value)
					}
				}
				return out, nil
			},
		}},
		Handler: cli.URIHandlerConfig{
			Vendor:      "hop-top",
			App:         "spaced",
			Language:    generate.LanguageGo,
			Scheme:      "spaced",
			AppPath:     "spaced",
			DisplayName: "spaced",
		},
	}
}

func normalizeSpacedCLI(root *cobra.Command) {
	if root == nil {
		return
	}
	var walk func(*cobra.Command, int)
	walk = func(c *cobra.Command, depth int) {
		if c == nil {
			return
		}
		if len(c.Commands()) > 0 && depth > 0 {
			cli.SetHierarchical(c)
		}
		if c.Runnable() {
			if c.Long == "" {
				c.Long = c.Short
			}
			if _, ok := cli.GetSideEffect(c); !ok {
				cli.SetSideEffect(c, spacedSideEffect(c))
			}
			if _, ok := cli.GetIdempotency(c); !ok {
				cli.SetIdempotency(c, cli.IdempotencyYes)
			}
			if depth == 1 {
				cli.SetTopLevelVerb(c)
			}
		}
		for _, child := range c.Commands() {
			walk(child, depth+1)
		}
	}
	walk(root, 0)
}

func spacedSideEffect(c *cobra.Command) cli.SideEffect {
	switch c.Name() {
	case "serve":
		return cli.SideEffectInteractive
	default:
		return cli.SideEffectRead
	}
}
