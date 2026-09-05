// Package cli — netglobals.go owns the family-wide --offline
// persistent global (cli-parity-guide, "Global Flags") and threads its
// resolved value through the command context so leaves consume it
// without reaching back into the Root.
//
// --offline disables network access. It is the highest-precedence
// override: per-command network opt-ins (peer discovery, sync
// replication, GitHub repo creation, initial push, upgrade checks)
// must behave as if their corresponding opt-out flag had been passed.
//
// Enforcement is not process-wide, because Go provides no hook beneath
// net.Dial. It covers net/http (netpolicy.Install) and any client whose
// dialer is routed through netpolicy.GuardDial — which now includes
// every egress path kit itself owns: SMTP, WebSocket and the TiDB
// driver. A dependency that opens its own socket and exposes no dialer
// hook is not covered; see the netpolicy package doc, "Scope", for the
// exact boundary.
// The override only forces opt-outs ON — it never un-sets an
// explicitly passed --no-* flag.

package cli

import (
	"context"

	"github.com/spf13/cobra"

	"hop.top/kit/go/core/netpolicy"
)

// Flag names for the session globals. Registered unconditionally in
// cli.New; the names are reserved, matching the delegation-safety
// globals (--confirm, --max-ops, --policy).
const (
	offlineFlag = "offline"
)

// WithOffline returns a context tagged with the offline marker.
// Installed by the netglobals hook before RunE dispatch; tests inject
// it directly when driving a leaf without a full Root.
//
// The marker itself lives in go/core/netpolicy so transports can
// enforce it without importing this package (which would cycle); these
// two functions are forwarders kept for the existing call sites.
func WithOffline(ctx context.Context, offline bool) context.Context {
	return netpolicy.WithOffline(ctx, offline)
}

// IsOffline reports whether ctx carries the offline marker. Untagged
// contexts (including nil-safe context.Background()) report false.
func IsOffline(ctx context.Context) bool {
	return netpolicy.IsOffline(ctx)
}

// Offline reports whether the kit-global --offline flag (or its
// viper config binding) is set. nil-safe.
func (r *Root) Offline() bool {
	if r == nil || r.Viper == nil {
		return false
	}
	return r.Viper.GetBool(offlineFlag)
}

// installNetGlobalsHook returns a PersistentPreRunE hook that stamps
// the resolved --offline value onto the command context before RunE
// dispatch. Composes into the kit chain in
// cli.New. Values left at their zero default add no tag, so untouched
// contexts stay clean.
func (r *Root) installNetGlobalsHook() func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		if r.Offline() {
			ctx = WithOffline(ctx, true)
		}
		cmd.SetContext(ctx)
		return nil
	}
}
