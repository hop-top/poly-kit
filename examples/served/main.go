// Command served is the conformance fixture for kit's zero-wiring
// serve capability: a kit CLI built with cli.New and a handful of
// options, with no transport mounted by hand.
//
// Its tests (served_test.go) drive the real Execute path — the one
// that installs the confirmation and policy gates — and assert every
// claim the serve-lifecycle contract makes about a conformant
// application command: the serve hierarchy exists, the api and socket
// services are listed, readiness reaches the bus and the log,
// discovery describes every command with the right reason, reads and
// writes run over REST and the socket, destructive commands are
// withheld until a surface is named and confirmed, interactive and
// self-hosting commands never run remotely, the api binds loopback
// and refuses unauthenticated remote serving, and an adopter service
// starts under the same supervisor.
//
// The command tree is deliberately small and covers one command per
// class the contract distinguishes:
//
//	item list    read               declares an output schema
//	item add     write-local
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

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/transport/cmdsurface"
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
}

// newRoot builds the fixture's root. This is the whole of the wiring
// an adopter writes: the root, the reserved status verb, the two
// kit-shipped services, one service of their own, and the commands.
func newRoot(opts options) *cli.Root {
	if opts.heartbeat == nil {
		opts.heartbeat = newHeartbeat()
	}
	policy := cmdsurface.Policy{AllowDestructiveOn: opts.allowDestructiveOn}

	root := cli.New(cli.Config{
		Name:    "served",
		Version: "0.1.0",
		Short:   "Conformance fixture for served commands",
	},
		cli.WithStatus(cli.StatusConfig{}),
		cli.WithAPI(cli.APIConfig{Policy: policy}),
		cli.WithSocket(cli.SocketConfig{Policy: policy}),
		cli.WithService(opts.heartbeat),
		cli.WithServiceBus(opts.bus),
	)

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

	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an item",
		Long:  "Add one item by name.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st.add(args[0])
			fmt.Fprintf(cmd.OutOrStdout(), "added %s\n", args[0])
			return nil
		},
	}
	cli.SetSideEffect(add, cli.SideEffectWriteLocal)
	cli.SetIdempotency(add, cli.IdempotencyYes)

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

	item.AddCommand(list, add, purge)
	return item
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
	root := newRoot(options{bus: bus.New()})
	if err := root.Execute(context.Background()); err != nil {
		os.Exit(exitCode(err))
	}
}
