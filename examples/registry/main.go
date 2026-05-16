// Command registry demonstrates service discovery for kit apps.
// Peers announce capabilities via mDNS; others browse and inspect.
//
// Usage:
//
//	registry announce --name myapp --addr :8080 --cap "users:crud" --cap "health:get"
//	registry browse                        # list discovered services
//	registry inspect <id>                  # GET /capabilities from a peer
//	registry watch                         # stream discovery events
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/peer"
	"hop.top/kit/go/transport/api"
)

const mdnsService = "_kit-registry._tcp"

func main() {
	root := cli.New(cli.Config{
		Name:    "registry",
		Version: "0.1.0",
		Short:   "Service discovery registry for kit apps",
	},
		cli.WithIdentity(cli.IdentityConfig{}),
		cli.WithPeers(cli.PeerConfig{
			Service:   mdnsService,
			Discovery: peer.NewMDNSDiscoverer(mdnsService),
		}),
	)

	root.Cmd.AddCommand(announceCmd(root), browseCmd(root), inspectCmd(root), watchCmd(root))

	if err := root.Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}

func announceCmd(r *cli.Root) *cobra.Command {
	var (
		name string
		addr string
		caps []string
	)
	cmd := &cobra.Command{
		Use:   "announce",
		Short: "Announce service with capabilities via mDNS",
		RunE: func(_ *cobra.Command, _ []string) error {
			pubPEM, _ := r.Identity.MarshalPublicKey()
			info := peer.PeerInfo{
				ID:        r.Identity.PublicKeyID(),
				Name:      name,
				Addrs:     []string{addr},
				PublicKey: pubPEM,
				Metadata:  map[string]string{"caps": strings.Join(caps, ",")},
			}

			disc := peer.NewMDNSDiscoverer(mdnsService)
			if err := disc.Announce(context.Background(), info); err != nil {
				return fmt.Errorf("announce: %w", err)
			}

			// Serve /capabilities endpoint.
			cs := toolspec.NewCapabilitySet(name, "1.0.0")
			for _, c := range caps {
				parts := strings.SplitN(c, ":", 2)
				capName := parts[0]
				capType := "endpoint"
				if len(parts) == 2 {
					capType = parts[1]
				}
				cs.Add(toolspec.Capability{Name: capName, Type: capType})
			}

			router := api.NewRouter(api.WithCapabilities(name, "1.0.0"))
			mux := http.NewServeMux()
			mux.Handle("/", router)
			// Override /capabilities with our custom set.
			mux.HandleFunc("GET /capabilities", func(w http.ResponseWriter, _ *http.Request) {
				data, err := cs.JSON()
				if err != nil {
					http.Error(w, "failed to marshal capabilities", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(data)
			})

			srv := &http.Server{Addr: addr, Handler: mux}
			fmt.Fprintf(os.Stderr, "announcing %q on %s with caps: %v\n", name, addr, caps)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			errCh := make(chan error, 1)
			go func() { errCh <- srv.ListenAndServe() }()

			select {
			case <-ctx.Done():
				_ = disc.Stop()
				return srv.Shutdown(context.Background())
			case err := <-errCh:
				return err
			}
		},
	}
	cmd.Flags().StringVar(&name, "name", "app", "Service name")
	cmd.Flags().StringVar(&addr, "addr", ":8080", "Listen address")
	cmd.Flags().StringSliceVar(&caps, "cap", nil, "Capabilities (name:type)")
	return cmd
}

func browseCmd(r *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "browse",
		Short: "Discover services on the local network",
		RunE: func(cmd *cobra.Command, _ []string) error {
			disc := peer.NewMDNSDiscoverer(mdnsService)
			peers, err := disc.Browse(context.Background())
			if err != nil {
				return err
			}
			if len(peers) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "no services found")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tADDR\tCAPS")
			for _, p := range peers {
				caps := p.Metadata["caps"]
				addrs := strings.Join(p.Addrs, ",")
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.ID, p.Name, addrs, caps)
			}
			return w.Flush()
		},
	}
}

func inspectCmd(_ *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <addr>",
		Short: "GET /capabilities from a peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			url := fmt.Sprintf("http://%s/capabilities", args[0])
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("inspect: %w", err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			var cs toolspec.CapabilitySet
			if err := json.Unmarshal(body, &cs); err != nil {
				// Print raw if not parseable.
				fmt.Println(string(body))
				return nil
			}

			fmt.Printf("Service: %s (v%s)\n", cs.ServiceName, cs.Version)
			for _, c := range cs.Capabilities {
				fmt.Printf("  %s [%s] %s\n", c.Name, c.Type, c.Path)
			}
			return nil
		},
	}
}

func watchCmd(r *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Stream peer discovery events",
		RunE: func(_ *cobra.Command, _ []string) error {
			r.Mesh.OnConnect(func(p peer.PeerInfo) {
				fmt.Printf("[+] %s %s (%s)\n", p.ID, p.Name, strings.Join(p.Addrs, ","))
			})
			r.Mesh.OnDisconnect(func(p peer.PeerInfo) {
				fmt.Printf("[-] %s %s\n", p.ID, p.Name)
			})

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			fmt.Fprintln(os.Stderr, "watching for peer events...")
			return r.Mesh.Start(ctx)
		},
	}
}
