package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"hop.top/kit/go/runtime/peer"
)

// Demo-only: uses static mock data. Replace with peer.NewMDNSDiscoverer for real LAN discovery.

// PeerCmd returns the `peer` command group for LAN peer discovery.
func PeerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peer",
		Short: "Discover and connect to peers",
		Long:  "Find other spaced instances on the local network.",
	}
	cmd.AddCommand(peerListCmd())
	cmd.AddCommand(peerConnectCmd())
	return cmd
}

func peerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List discovered peers on LAN",
		RunE: func(cmd *cobra.Command, _ []string) error {
			disc := &peer.StaticDiscoverer{
				Peers: []peer.PeerInfo{
					{ID: "demo-node-1", Name: "houston", Addrs: []string{"192.168.1.42:8080"}},
					{ID: "demo-node-2", Name: "boca-chica", Addrs: []string{"192.168.1.99:8080"}},
				},
			}
			peers, err := disc.Browse(cmd.Context())
			if err != nil {
				return err
			}
			if len(peers) == 0 {
				fmt.Println("  No peers found.")
				return nil
			}
			fmt.Printf("  %-16s %-12s %s\n", "ID", "NAME", "ADDR")
			for _, p := range peers {
				addr := ""
				if len(p.Addrs) > 0 {
					addr = p.Addrs[0]
				}
				fmt.Printf("  %-16s %-12s %s\n", p.ID, p.Name, addr)
			}
			return nil
		},
	}
}

func peerConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "connect <addr>",
		Short: "Connect to a peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			addr := args[0]
			fmt.Printf("  Connecting to peer at %s...\n", addr)
			fmt.Printf("  Connected (demo — no real connection established). Ready to sync missions with %s.\n", addr)
			return nil
		},
	}
}
