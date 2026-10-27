// Package peers adds peer discovery to a cli.Root: the peer mesh, its
// registry and trust manager, and the `peer` command group. It attaches
// through the Root's extension slot (cli.Extend), so a CLI that does
// not discover peers links neither mDNS nor the peer store's SQLite.
//
//	root := cli.New(cfg, cli.WithIdentity(cli.IdentityConfig{}), peers.With(peers.Config{}))
//	if m := peers.Of(root).Mesh; m != nil {
//		go m.Start(ctx)
//	}
package peers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/internal/serving"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/util"
	"hop.top/kit/go/runtime/peer"
	"hop.top/kit/go/storage/sqlstore"
)

// Config configures peer mesh management.
type Config struct {
	// Discovery overrides the default discoverer. Nil = static (no-op).
	Discovery peer.Discoverer
	// Service is the mDNS service name (default "_kit._tcp").
	Service string
	// DataDir overrides the peer data directory.
	// Default: $XDG_DATA_HOME/kit/peers/
	DataDir string
}

// Peers is a Root's peer mesh, registry and trust manager. They stay
// nil until cli.New has initialized them, and stay nil when that
// failed; the root's PersistentPreRunE then returns the failure.
type Peers struct {
	// Mesh is the peer mesh.
	Mesh *peer.Mesh
	// Registry is the peer registry the mesh and the `peer` commands
	// read and write.
	Registry *peer.Registry
	// Trust is the trust manager over Registry.
	Trust *peer.TrustManager

	cfg Config
}

var key = cli.NewExtensionKey[*Peers]("peers")

// With enables peer mesh management on the root. It requires
// cli.WithIdentity, in either order: cli.New initializes the peers once
// every option has run and the identity keypair is resolved. A second
// With replaces the first one's Config.
func With(cfg Config) func(*cli.Root) {
	return func(r *cli.Root) {
		p := cli.Extend(r, key, func(*cli.Root) *Peers { return &Peers{} })
		p.cfg = cfg
		// The mesh puts the Root in front of other processes: for the
		// kit token command it counts as a serve option. token verify
		// needs authn, so it mounts beside the signing leaves only in a
		// binary that imports go/console/cli/service, which registers
		// it; peers deliberately links no serve plane. Peer challenges
		// sign with the same identity keypair (identity.SignJWT);
		// nothing about peer trust changes.
		serving.Mark(r)
	}
}

// Of returns r's peers. It never returns nil: without With, on a Root
// not built by cli.New, on a nil Root, or when initialization failed,
// Mesh, Registry and Trust are nil, as the Root fields they replace
// were. Test the field, not the result: peers.Of(root).Mesh != nil. Of
// attaches nothing.
func Of(r *cli.Root) *Peers {
	if p, ok := cli.Extension(r, key); ok {
		return p
	}
	return &Peers{}
}

var _ cli.InitHook = (*Peers)(nil)

// Init creates the registry, trust manager and mesh and mounts the
// `peer` commands. cli.New calls it (cli.InitHook) after every option
// has run and the identity keypair is resolved, and defers an error to
// the root's PersistentPreRunE, so --help and completion still work.
// The `peer` group is mounted after New's snapshot of reserved
// subcommands, so it is not kit-reserved.
func (p *Peers) Init(r *cli.Root) error {
	cfg := p.cfg

	if r.Identity == nil {
		return fmt.Errorf("peer: peers.With requires cli.WithIdentity")
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

	pubPEM, _ := r.Identity.MarshalPublicKey()
	self := peer.PeerInfo{
		ID:        r.Identity.PublicKeyID(),
		PublicKey: pubPEM,
	}

	p.Registry = registry
	p.Trust = tm
	p.Mesh = peer.NewMesh(self, tm, disc)

	r.Cmd.AddCommand(peerCmd(r, p))
	// Mounted after cli.New takes its snapshot of the commands kit
	// owns, so reserved by name here. Reserved, it is management-only:
	// no served surface lists, trusts, blocks or revokes a peer, and the
	// trust-on-first-use step stays a person's on the command line.
	r.MarkReserved("peer")
	return nil
}

func peerCmd(r *cli.Root, p *Peers) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peer",
		Short: "Manage mesh peers",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(
		peerListCmd(r, p),
		peerTrustCmd(r, p),
		peerBlockCmd(r, p),
		peerRevokeCmd(r, p),
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

func peerListCmd(r *cli.Root, p *Peers) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show known peers",
		Long: "List every peer in the local mesh registry with its " +
			"trust state (trusted|blocked|pending), advertised " +
			"addresses, and last-seen timestamp.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			records, err := p.Registry.List()
			if err != nil {
				return err
			}
			format := resolvePeerListFormat(cmd)
			// A data format gets its document even when it is empty:
			// `[]`, not silence a parser has to special-case.
			if len(records) == 0 && format != output.JSON && format != output.YAML {
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
			return output.Render(r.Streams.Data, format, rows)
		},
	}
	cli.SetSideEffect(cmd, cli.SideEffectRead)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	_ = cli.SetOutputSchema(cmd, cli.OutputSchema{Type: &[]peerRow{}, Version: "1.0"})
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

// peerTrustRow is what peer trust, block and revoke render: the peer
// and the trust state the registry holds for it afterwards, under
// peerRow's field names and labels.
type peerTrustRow struct {
	ID    string `json:"id"    yaml:"id"    table:"ID,priority=9"`
	Trust string `json:"trust" yaml:"trust" table:"Trust,priority=6"`
}

// renderPeerTrust reads id back from the registry after a trust change
// and renders the state it was left in.
func renderPeerTrust(cmd *cobra.Command, r *cli.Root, p *Peers, id string) error {
	rec, err := p.Registry.Get(id)
	if err != nil {
		return err
	}
	if rec == nil {
		return output.NotFoundError(fmt.Sprintf("peer: %s not found", id))
	}
	return output.Dispatch(cmd, r.Viper, peerTrustRow{ID: rec.ID, Trust: trustLabel(rec.Trust)})
}

// planPeerTrust is peer trust, block and revoke under --dry-run: it
// reads the peer as the real run would, so an unknown peer fails the
// same way, and plans the trust change without writing it.
func planPeerTrust(cmd *cobra.Command, p *Peers, id string, to peer.TrustLevel) error {
	rec, err := p.Registry.Get(id)
	if err != nil {
		return err
	}
	if rec == nil {
		return output.NotFoundError(fmt.Sprintf("peer: %s not found", id))
	}
	return cli.RenderPlan(cmd, cli.Plan{
		Args: map[string]any{"id": id},
		Effects: []cli.Effect{{Kind: "update", Target: "peer:" + id, Reversible: true,
			Detail: "trust " + trustLabel(rec.Trust) + " -> " + trustLabel(to)}},
	})
}

// setPeerTrustSchema declares the shape renderPeerTrust prints.
func setPeerTrustSchema(cmd *cobra.Command) {
	_ = cli.SetOutputSchema(cmd, cli.OutputSchema{Type: &peerTrustRow{}, Version: "1.0"})
}

func peerTrustCmd(r *cli.Root, p *Peers) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust <id>",
		Short: "Explicitly trust a peer",
		Long: "Move the named peer from pending-TOFU to trusted in the " +
			"local trust manager. Trusting is idempotent — re-trusting " +
			"an already-trusted peer is a no-op.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cli.IsDryRun(cmd) {
				return planPeerTrust(cmd, p, args[0], peer.Trusted)
			}
			if err := p.Trust.Trust(args[0]); err != nil {
				return err
			}
			return renderPeerTrust(cmd, r, p, args[0])
		},
	}
	cli.SetSideEffect(cmd, cli.SideEffectWrite)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	setPeerTrustSchema(cmd)
	return cmd
}

func peerBlockCmd(r *cli.Root, p *Peers) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "block <id>",
		Short: "Block a peer",
		Long: "Mark the named peer as blocked in the local trust " +
			"manager; the mesh refuses connections from blocked peers " +
			"until explicitly trusted again.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cli.IsDryRun(cmd) {
				return planPeerTrust(cmd, p, args[0], peer.Blocked)
			}
			if err := p.Trust.Block(args[0]); err != nil {
				return err
			}
			return renderPeerTrust(cmd, r, p, args[0])
		},
	}
	cli.SetSideEffect(cmd, cli.SideEffectWrite)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	setPeerTrustSchema(cmd)
	return cmd
}

func peerRevokeCmd(r *cli.Root, p *Peers) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke trust from a peer",
		Long: "Return the named peer to the pending-TOFU state. Future " +
			"first-contact connections will require re-trusting via " +
			"`peer trust`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cli.IsDryRun(cmd) {
				return planPeerTrust(cmd, p, args[0], peer.Unknown)
			}
			if err := p.Trust.Revoke(args[0]); err != nil {
				return err
			}
			return renderPeerTrust(cmd, r, p, args[0])
		},
	}
	cli.SetSideEffect(cmd, cli.SideEffectWrite)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	setPeerTrustSchema(cmd)
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
