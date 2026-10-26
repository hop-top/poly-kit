// Command relay demonstrates cross-machine event relay using the kit bus
// with WebSocket-based network transport, authenticated with
// self-signed tokens: each relay has an identity keypair, a connecting
// relay signs its handshake token with it, and a listener accepts the
// relays whose public keys it trusts.
//
// Usage:
//
//	relay pubkey > me.pub                          # this relay's public key
//	relay listen --addr :9090 --trust peer.pub     # accept trusted peers
//	relay connect <addr> --auth [--topics "app.*"] # connect, signed
//	relay publish <topic> <payload>                # publish event
//	relay subscribe <topic>                        # subscribe and print
//	relay token create --sub me                    # mint a self-signed token
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/bus"
)

func main() {
	root := newRoot(cli.Config{
		Name:    "relay",
		Version: "0.1.0",
		Short:   "Cross-machine event relay over WebSocket",
	}, cli.IdentityConfig{})

	// Single bus instance shared by all commands.
	eventBus := bus.New(bus.WithNetwork())
	addCommands(root, eventBus)

	if err := root.Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}

// newRoot builds the relay's root with its identity keypair: the key
// that signs its bus handshake, and the one that mounts the kit's
// self-signed `token create|decode|claims`.
func newRoot(cfg cli.Config, id cli.IdentityConfig) *cli.Root {
	return cli.New(cfg, cli.WithIdentity(id))
}

func addCommands(root *cli.Root, eventBus bus.Bus) {
	root.Cmd.AddCommand(listenCmd(eventBus), connectCmd(root, eventBus), publishCmd(eventBus),
		subscribeCmd(eventBus), pubkeyCmd(root))
}

func pubkeyCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "pubkey",
		Short: "Print this relay's public key (PEM) for a listener to --trust",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if root.Identity == nil {
				return errors.New("relay has no identity keypair")
			}
			pem, err := root.Identity.MarshalPublicKey()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(pem)
			return err
		},
	}
}

func listenCmd(eventBus bus.Bus) *cobra.Command {
	var (
		addr  string
		trust []string
	)
	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Accept incoming WebSocket bus peers",
		RunE: func(_ *cobra.Command, _ []string) error {
			var opts []bus.NetworkOption
			if len(trust) > 0 {
				keys, err := loadTrusted(trust)
				if err != nil {
					return err
				}
				opts = append(opts, bus.WithAuth(newRelayAuth(nil, keys...)))
			} else {
				fmt.Fprintln(os.Stderr, "warning: no --trust: accepting unauthenticated peers")
			}
			na := bus.NewNetworkAdapter(eventBus, opts...)

			mux := http.NewServeMux()
			mux.Handle("/bus", na.Handler())

			srv := &http.Server{Addr: addr, Handler: mux}
			fmt.Fprintf(os.Stderr, "listening on %s/bus\n", addr)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			errCh := make(chan error, 1)
			go func() { errCh <- srv.ListenAndServe() }()

			select {
			case <-ctx.Done():
				return srv.Shutdown(context.Background())
			case err := <-errCh:
				return err
			}
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":9090", "Listen address")
	cmd.Flags().StringSliceVar(&trust, "trust", nil,
		"Public key files (PEM, from `relay pubkey`) of the relays allowed to connect; "+
			"each must connect with --auth")
	return cmd
}

func connectCmd(root *cli.Root, eventBus bus.Bus) *cobra.Command {
	var (
		topics string
		auth   bool
	)
	cmd := &cobra.Command{
		Use:   "connect <addr>",
		Short: "Connect bus to a remote peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var opts []bus.NetworkOption
			if topics != "" {
				opts = append(opts, bus.WithFilter(bus.TopicFilter{
					Allow: []string{topics},
				}))
			}
			if auth {
				if root.Identity == nil {
					return errors.New("connect --auth: relay has no identity keypair")
				}
				opts = append(opts, bus.WithAuth(newRelayAuth(root.Identity)))
			}

			na := bus.NewNetworkAdapter(eventBus, opts...)
			if err := na.Connect(context.Background(), args[0]); err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			fmt.Fprintf(os.Stderr, "connected to %s\n", args[0])

			// Block until interrupt.
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			<-ctx.Done()
			return na.Close()
		},
	}
	cmd.Flags().StringVar(&topics, "topics", "", "Topic filter pattern (e.g. \"app.*\")")
	cmd.Flags().BoolVar(&auth, "auth", false,
		"Authenticate with a token signed by this relay's identity keypair (listener uses --trust)")
	return cmd
}

func publishCmd(eventBus bus.Bus) *cobra.Command {
	return &cobra.Command{
		Use:   "publish <topic> <payload>",
		Short: "Publish an event to the bus",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			e := bus.NewEvent(bus.Topic(args[0]), "relay-cli", args[1])
			if err := eventBus.Publish(context.Background(), e); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "published %s\n", args[0])
			return nil
		},
	}
}

func subscribeCmd(eventBus bus.Bus) *cobra.Command {
	return &cobra.Command{
		Use:   "subscribe <topic>",
		Short: "Subscribe to a topic and print events",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pattern := args[0]
			unsub := eventBus.Subscribe(pattern, func(_ context.Context, e bus.Event) error {
				fmt.Printf("[%s] %s: %v\n",
					e.Timestamp.Format(time.RFC3339), e.Topic, e.Payload)
				return nil
			})
			defer unsub()

			fmt.Fprintf(os.Stderr, "subscribed to %q, waiting...\n", pattern)
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			<-ctx.Done()
			return nil
		},
	}
}
