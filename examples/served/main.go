// Command served is the conformance fixture for kit's zero-wiring
// serve capability: a kit CLI built with cli.New and a handful of
// options, with no transport mounted by hand.
//
// Its tests (served_test.go, mcp_test.go) drive the real Execute path
// — the one that installs the confirmation and policy gates — and
// assert every claim the serve-lifecycle contract makes about a
// conformant application command: the serve hierarchy exists, the
// api, socket, mcp and rpc services are listed, readiness reaches the bus and the log,
// discovery describes every command with the right reason, reads and
// writes run over REST, the socket and RPC, destructive commands are
// withheld until a surface is named and confirmed, interactive and
// self-hosting commands never run remotely, the api binds loopback
// and refuses unauthenticated remote serving, and an adopter service
// starts under the same supervisor.
//
// The command tree is deliberately small and covers one command per
// class the contract distinguishes:
//
//	item list    read               declares an output schema
//	item watch   read               long-running: streams until done or canceled
//	item add     write-local
//	item tag     write-local        kit/requires-confirmation
//	item sync    read               kit/auth-required
//	item export  read               kit/permissions: runs for a caller holding items:export
//	item purge   destructive-shared
//	shell        interactive
//	upgrade      write, kit/self-hosting
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/celpermission"
	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/console/cli/rpcserve"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/observability"

	// The API key store's driver: services.<svc>.auth.mode: apikey
	// keeps keys in sqlite unless configured otherwise.
	_ "hop.top/kit/go/storage/kv/sqlite"
)

// Item is one row of `item list`. The json tags name the fields in
// `data`; the table tags name the columns on the CLI.
type Item struct {
	Name string `json:"name" table:"NAME"`
}

// store is the fixture's state: an in-memory set of item names.
type store struct {
	mu    sync.Mutex
	items map[string]struct{}
}

func newStore() *store {
	return &store{items: map[string]struct{}{"bolt": {}, "nut": {}}}
}

func (s *store) list() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.items))
	for n := range s.items {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Item, 0, len(names))
	for _, n := range names {
		out = append(out, Item{Name: n})
	}
	return out
}

func (s *store) add(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[name] = struct{}{}
}

func (s *store) purge() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.items)
	s.items = map[string]struct{}{}
	return n
}

// options is what a test varies about the fixture. The zero value is
// the posture main ships: the default policy on every surface, and no
// bus wired.
type options struct {
	// bus receives the serve lifecycle events. Tests subscribe to it
	// to observe readiness; nil publishes nothing.
	bus bus.Bus
	// allowDestructiveOn names the served surfaces on which
	// destructive commands may run. Empty is the safe default: none.
	allowDestructiveOn []cmdsurface.Surface
	// heartbeat is the adopter-owned service registered beside api and
	// socket, so a test can observe it start.
	heartbeat *heartbeat
	// observe is the tracing and metrics provider. nil links the
	// default one, which configuration leaves off.
	observe *observability.Serve
	// config is set on the root's configuration before it runs, the
	// way a config file would set it.
	config map[string]any
	// identity gives the tool an identity keypair, which `token create`
	// signs with and services.<svc>.auth.mode: jwt trusts. nil gives it
	// none.
	identity *cli.IdentityConfig
	// apiKeys is the API key store's file; empty is the default under
	// the data dir.
	apiKeys string
}

// newRoot builds the fixture's root. This is the whole of the wiring
// an adopter writes: the root, the reserved status and audit verbs,
// --policy files from $XDG_CONFIG_HOME/served/policies with their
// permissions: rules evaluated, the four kit-shipped services, one
// service of their own, the observability provider an operator can
// turn on, the identity keypair tokens are signed with, and the
// commands.
func newRoot(opts options) *cli.Root {
	if opts.heartbeat == nil {
		opts.heartbeat = newHeartbeat()
	}
	if opts.observe == nil {
		opts.observe = observability.NewServe()
	}
	policy := cmdsurface.Policy{AllowDestructiveOn: opts.allowDestructiveOn}

	wiring := []func(*cli.Root){
		cli.WithStatus(cli.StatusConfig{}),
		cli.WithAuditCommand(),
		cli.WithPolicy(cli.DefaultPolicyLoader("served")),
		celpermission.With(),
		cli.WithAPI(cli.APIConfig{Policy: policy}),
		cli.WithSocket(cli.SocketConfig{Policy: policy}),
		mcpserve.With(mcpserve.Config{Policy: policy}),
		rpcserve.With(rpcserve.Config{Policy: policy}),
		cli.WithService(opts.heartbeat),
		cli.WithServiceBus(opts.bus),
		cli.WithObservability(opts.observe),
		cli.WithAPIKeys(cli.APIKeysConfig{Path: opts.apiKeys}),
	}
	if opts.identity != nil {
		// The tool's keypair: `token create` signs with it, and
		// services.<svc>.auth.mode: jwt trusts it.
		wiring = append(wiring, cli.WithIdentity(*opts.identity))
	}
	root := cli.New(cli.Config{
		Name:    "served",
		Version: "0.1.0",
		Short:   "Conformance fixture for served commands",
	}, wiring...)
	for k, v := range opts.config {
		root.Viper.Set(k, v)
	}

	st := newStore()
	root.Cmd.AddCommand(itemCmd(root, st), shellCmd(), upgradeCmd())
	return root
}

func itemCmd(root *cli.Root, st *store) *cobra.Command {
	item := &cobra.Command{Use: "item", Short: "Manage items"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List items",
		Long:  "List every item, as a table or as data.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return output.Dispatch(cmd, root.Viper, st.list())
		},
	}
	cli.SetSideEffect(list, cli.SideEffectRead)
	cli.SetIdempotency(list, cli.IdempotencyYes)
	if err := cli.SetOutputSchema(list, cli.OutputSchema{Type: &[]Item{}, Version: "1.0"}); err != nil {
		panic(err)
	}

	// add takes its operand positionally and declares it in kit/args,
	// which is what lets a transport that publishes a schema carry
	// it: REST and the socket as "args", MCP as the "args" property.
	add := &cobra.Command{
		Use:         "add <name>",
		Short:       "Add an item",
		Long:        "Add one item by name.",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{"kit/args": "name"},
		RunE: func(cmd *cobra.Command, args []string) error {
			st.add(args[0])
			fmt.Fprintf(cmd.OutOrStdout(), "added %s\n", args[0])
			return nil
		},
	}
	cli.SetSideEffect(add, cli.SideEffectWriteLocal)
	cli.SetIdempotency(add, cli.IdempotencyYes)

	// tag declares kit/requires-confirmation: over MCP a person
	// approves each call. It takes its operand as a named flag, the
	// counterpart to add's positional one.
	tag := &cobra.Command{
		Use:         "tag",
		Short:       "Tag an item",
		Long:        "Tag one item by name. Over MCP a person approves each call.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"kit/requires-confirmation": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			fmt.Fprintf(cmd.OutOrStdout(), "tagged %s\n", name)
			return nil
		},
	}
	tag.Flags().String("name", "", "Item to tag")
	cli.SetSideEffect(tag, cli.SideEffectWriteLocal)
	cli.SetIdempotency(tag, cli.IdempotencyYes)

	// sync declares kit/auth-required: it acts with the caller's
	// credentials.
	syncCmd := &cobra.Command{
		Use:         "sync",
		Short:       "Sync items with the caller's account",
		Long:        "Sync every item with the caller's account. Needs the caller's credentials.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"kit/auth-required": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "synced %d items\n", len(st.list()))
			return nil
		},
	}
	cli.SetSideEffect(syncCmd, cli.SideEffectRead)
	cli.SetIdempotency(syncCmd, cli.IdempotencyYes)

	// export declares kit/permissions: over a served surface it runs
	// only for a caller whose verified credential holds items:export,
	// or one the transport established as the owner (the socket, MCP
	// over stdio).
	export := &cobra.Command{
		Use:         "export",
		Short:       "Export every item",
		Long:        "Export every item. Needs the items:export scope when served.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"kit/permissions": "items:export"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "exported %d items\n", len(st.list()))
			return nil
		},
	}
	cli.SetSideEffect(export, cli.SideEffectRead)
	cli.SetIdempotency(export, cli.IdempotencyYes)

	purge := &cobra.Command{
		Use:   "purge",
		Short: "Remove every item",
		Long:  "Remove every item. Destructive: withheld from served surfaces by default.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "purged %d items\n", st.purge())
			return nil
		},
	}
	cli.SetSideEffect(purge, cli.SideEffectDestructiveShared)
	cli.SetIdempotency(purge, cli.IdempotencyYes)

	item.AddCommand(list, watchCmd(st), add, tag, syncCmd, export, purge)
	return item
}

// watchCmd is the long-running read: it reports the item count once
// per interval until it has reported count times, or forever when
// count is 0, and stops as soon as its context is canceled. Served,
// it is what the streaming route is for: GET
// /v1/commands/item/watch/stream delivers each line as it is written,
// and a client that disconnects cancels it.
func watchCmd(st *store) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Report the item count until stopped",
		Long:  "Report the item count once per interval, count times, or until canceled when count is 0.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			count, _ := cmd.Flags().GetInt("count")
			interval, _ := cmd.Flags().GetDuration("interval")
			tick := time.NewTicker(interval)
			defer tick.Stop()
			for i := 1; count == 0 || i <= count; i++ {
				fmt.Fprintf(cmd.OutOrStdout(), "tick %d: %d items\n", i, len(st.list()))
				if count != 0 && i == count {
					return nil
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-tick.C:
				}
			}
			return nil
		},
	}
	cmd.Flags().Int("count", 0, "stop after this many reports; 0 runs until canceled")
	cmd.Flags().Duration("interval", time.Second, "time between reports")
	cli.SetSideEffect(cmd, cli.SideEffectRead)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	return cmd
}

// shellCmd is the interactive class: it needs a terminal and a human,
// so no transport may run it.
func shellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Open an interactive shell",
		Long:  "Open an interactive shell over the item store.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "shell")
			return nil
		},
	}
	cli.SetSideEffect(cmd, cli.SideEffectInteractive)
	cli.SetIdempotency(cmd, cli.IdempotencyNo)
	cli.SetTopLevelVerb(cmd)
	return cmd
}

// upgradeCmd is the self-hosting class an adopter marks itself: a
// command that would replace the binary that is serving.
func upgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "upgrade",
		Short:       "Replace this binary with the latest release",
		Long:        "Replace this binary with the latest release. Self-hosting: runs from the CLI only.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"kit/self-hosting": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "upgraded")
			return nil
		},
	}
	cli.SetSideEffect(cmd, cli.SideEffectWrite)
	cli.SetIdempotency(cmd, cli.IdempotencyNo)
	cli.SetTopLevelVerb(cmd)
	return cmd
}

// exitCode maps the root's error onto the process exit code the kit
// taxonomy assigns: kit's structured errors carry their own.
func exitCode(err error) int {
	var kitErr *output.Error
	if errors.As(err, &kitErr) && kitErr.ExitCode != 0 {
		return kitErr.ExitCode
	}
	return 1
}

func main() {
	root := newRoot(options{bus: bus.New(), identity: &cli.IdentityConfig{}})
	if err := root.Execute(context.Background()); err != nil {
		os.Exit(exitCode(err))
	}
}
