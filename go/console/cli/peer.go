package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/util"
	"hop.top/kit/go/runtime/peer"
	"hop.top/kit/go/storage/sqlstore"
)

// PeerConfig configures peer mesh management.
type PeerConfig struct {
	// Discovery overrides the default discoverer. Nil = static (no-op).
	Discovery peer.Discoverer
	// Service is the mDNS service name (default "_kit._tcp").
	Service string
	// DataDir overrides the peer data directory.
	// Default: $XDG_DATA_HOME/kit/peers/
	DataDir string
}

// WithPeers enables peer mesh management on the CLI root.
func WithPeers(cfg PeerConfig) func(*Root) {
	return func(r *Root) {
		r.peerCfg = &cfg
	}
}

// initPeers creates the registry, trust manager, mesh and attaches commands.
func (r *Root) initPeers() error {
	cfg := r.peerCfg
	if cfg == nil {
		return nil
	}

	if r.Identity == nil {
		return fmt.Errorf("peer: WithPeers requires WithIdentity")
	}

	dataDir := cfg.DataDir
	if dataDir == "" {
		xdg := os.Getenv("XDG_DATA_HOME")
		if xdg == "" {
			home, _ := os.UserHomeDir()
			xdg = filepath.Join(home, ".local", "share")
		}
		dataDir = filepath.Join(xdg, "kit", "peers")
	}

	store, err := sqlstore.Open(filepath.Join(dataDir, "peers.db"), sqlstore.Options{})
	if err != nil {
		return fmt.Errorf("peer store: %w", err)
	}

	registry := peer.NewRegistry(store)
	disc := cfg.Discovery
	if disc == nil {
		disc = &peer.StaticDiscoverer{}
	}

	tm := peer.NewTrustManager(registry, r.Identity)

	// Build self PeerInfo from identity if available.
	var self peer.PeerInfo
	if r.Identity != nil {
		pubPEM, _ := r.Identity.MarshalPublicKey()
		self = peer.PeerInfo{
			ID:        r.Identity.PublicKeyID(),
			PublicKey: pubPEM,
		}
	}

	r.PeerRegistry = registry
	r.PeerTrust = tm
	r.Mesh = peer.NewMesh(self, tm, disc)

	r.Cmd.AddCommand(peerCmd(r))
	return nil
}

func peerCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peer",
		Short: "Manage mesh peers",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(
		peerListCmd(r),
		peerTrustCmd(r),
		peerBlockCmd(r),
		peerRevokeCmd(r),
	)
	return cmd
}

// peerRow is the rendered shape of one row in `peer list`. Field
// order, tags, and labels match the legacy text-aligned output (ID,
// NAME, ADDRS, TRUST, LAST SEEN); routing through output.Render gives
// JSON/YAML/CSV/text for free.
type peerRow struct {
	ID       string `json:"id"        yaml:"id"        table:"ID,priority=9"`
	Name     string `json:"name"      yaml:"name"      table:"Name,priority=8"`
	Addrs    string `json:"addrs"     yaml:"addrs"     table:"Addrs,priority=7"`
	Trust    string `json:"trust"     yaml:"trust"     table:"Trust,priority=6"`
	LastSeen string `json:"last_seen" yaml:"last_seen" table:"Last Seen,priority=5"`
}

func peerListCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show known peers",
		Long: "List every peer in the local mesh registry with its " +
			"trust state (trusted|blocked|pending), advertised " +
			"addresses, and last-seen timestamp.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			records, err := r.PeerRegistry.List()
			if err != nil {
				return err
			}
			if len(records) == 0 {
				fmt.Fprintln(r.Streams.Human, "No peers found.")
				return nil
			}
			rows := make([]peerRow, 0, len(records))
			for _, rec := range records {
				rows = append(rows, peerRow{
					ID:       rec.ID,
					Name:     rec.Name,
					Addrs:    strings.Join(rec.Addrs, ","),
					Trust:    trustLabel(rec.Trust),
					LastSeen: util.RelativeTime(rec.LastSeen),
				})
			}
			format := resolvePeerListFormat(cmd)
			return output.Render(r.Streams.Data, format, rows)
		},
	}
	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	return cmd
}

// resolvePeerListFormat returns the active --format value for the
// command, falling back to "table" when the flag is unregistered or
// unset.
func resolvePeerListFormat(cmd *cobra.Command) output.Format {
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.Flags().Lookup("format"); f != nil {
			if v := f.Value.String(); v != "" {
				return v
			}
		}
		if pf := c.PersistentFlags().Lookup("format"); pf != nil {
			if v := pf.Value.String(); v != "" {
				return v
			}
		}
	}
	return output.Table
}

func peerTrustCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust <id>",
		Short: "Explicitly trust a peer",
		Long: "Move the named peer from pending-TOFU to trusted in the " +
			"local trust manager. Trusting is idempotent — re-trusting " +
			"an already-trusted peer is a no-op.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return r.PeerTrust.Trust(args[0])
		},
	}
	SetSideEffect(cmd, SideEffectWrite)
	SetIdempotency(cmd, IdempotencyYes)
	return cmd
}

func peerBlockCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "block <id>",
		Short: "Block a peer",
		Long: "Mark the named peer as blocked in the local trust " +
			"manager; the mesh refuses connections from blocked peers " +
			"until explicitly trusted again.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return r.PeerTrust.Block(args[0])
		},
	}
	SetSideEffect(cmd, SideEffectWrite)
	SetIdempotency(cmd, IdempotencyYes)
	return cmd
}

func peerRevokeCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke trust from a peer",
		Long: "Return the named peer to the pending-TOFU state. Future " +
			"first-contact connections will require re-trusting via " +
			"`peer trust`.",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return r.PeerTrust.Revoke(args[0])
		},
	}
	SetSideEffect(cmd, SideEffectWrite)
	SetIdempotency(cmd, IdempotencyYes)
	return cmd
}

func trustLabel(t peer.TrustLevel) string {
	switch t {
	case peer.Trusted:
		return "trusted"
	case peer.Blocked:
		return "blocked"
	case peer.PendingTOFU:
		return "pending"
	default:
		return "unknown"
	}
}
