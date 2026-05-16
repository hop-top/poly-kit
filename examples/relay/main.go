// Command relay demonstrates cross-machine event relay using the kit bus
// with WebSocket-based network transport.
//
// Usage:
//
//	relay listen --addr :9090              # accept incoming peers
//	relay connect <addr> [--topics "app.*"] # connect to peer
//	relay publish <topic> <payload>        # publish event
//	relay subscribe <topic>                # subscribe and print
package main

import (
	"context"
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
	root := cli.New(cli.Config{
		Name:    "relay",
		Version: "0.1.0",
		Short:   "Cross-machine event relay over WebSocket",
	},
		// Identity configured for future auth integration. Currently no auth on bus connections.
		cli.WithIdentity(cli.IdentityConfig{}),
	)

	// Single bus instance shared by all commands.
	eventBus := bus.New(bus.WithNetwork())

	root.Cmd.AddCommand(listenCmd(eventBus), connectCmd(eventBus), publishCmd(eventBus), subscribeCmd(eventBus))

	if err := root.Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}

func listenCmd(eventBus bus.Bus) *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Accept incoming WebSocket bus peers",
		RunE: func(_ *cobra.Command, _ []string) error {
			na := bus.NewNetworkAdapter(eventBus)

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
	return cmd
}

func connectCmd(eventBus bus.Bus) *cobra.Command {
	var topics string
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
