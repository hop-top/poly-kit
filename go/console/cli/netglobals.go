// Package cli — netglobals.go owns the family-wide --offline /
// --profile / --instance persistent globals (cli-parity-guide, "Global
// Flags") and threads their resolved values through the command
// context so leaves consume them without reaching back into the Root.
//
// --offline disables all network access. It is the highest-precedence
// override: per-command network opt-ins (peer discovery, sync
// replication, GitHub repo creation, initial push, upgrade checks)
// must behave as if their corresponding opt-out flag had been passed.
// The override only forces opt-outs ON — it never un-sets an
// explicitly passed --no-* flag.
//
// --profile selects the active identity profile (credentials, default
// org, git author). Falls back to $APS_PROFILE when the flag is
// absent.
//
// --instance names a backend endpoint bundle resolved from
// $XDG_CONFIG_HOME/<tool>/instances.yaml. The flag is a pure resolver
// input: kit stamps the name on the context; subcommands that talk to
// a backing service opt in by reading it. Local-only subcommands
// ignore it.

package cli

import (
	"context"

	"github.com/spf13/cobra"
)

// Flag names for the session globals. Registered unconditionally in
// cli.New; the names are reserved, matching the delegation-safety
// globals (--confirm, --max-ops, --policy).
const (
	offlineFlag  = "offline"
	profileFlag  = "profile"
	instanceFlag = "instance"
)

// profileEnv is the environment variable consulted when --profile is
// not passed (cli-parity-guide: "Defaults to $APS_PROFILE").
const profileEnv = "APS_PROFILE"

// Unexported context-key types so external packages cannot collide
// with the tags; access goes through the With*/Is*/‑From helpers.
type (
	offlineCtxKey  struct{}
	profileCtxKey  struct{}
	instanceCtxKey struct{}
)

// WithOffline returns a context tagged with the offline marker.
// Installed by the netglobals hook before RunE dispatch; tests inject
// it directly when driving a leaf without a full Root.
func WithOffline(ctx context.Context, offline bool) context.Context {
	return context.WithValue(ctx, offlineCtxKey{}, offline)
}

// IsOffline reports whether ctx carries the offline marker. Untagged
// contexts (including nil-safe context.Background()) report false.
func IsOffline(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	on, _ := ctx.Value(offlineCtxKey{}).(bool)
	return on
}

// WithProfile returns a context carrying the active profile name.
func WithProfile(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, profileCtxKey{}, name)
}

// ProfileFrom returns the profile name stamped on ctx, or "" when
// none was resolved.
func ProfileFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	name, _ := ctx.Value(profileCtxKey{}).(string)
	return name
}

// WithInstance returns a context carrying the backend-instance name.
func WithInstance(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, instanceCtxKey{}, name)
}

// InstanceFrom returns the instance name stamped on ctx, or "" when
// none was resolved.
func InstanceFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	name, _ := ctx.Value(instanceCtxKey{}).(string)
	return name
}

// Offline reports whether the kit-global --offline flag (or its
// viper config binding) is set. nil-safe.
func (r *Root) Offline() bool {
	if r == nil || r.Viper == nil {
		return false
	}
	return r.Viper.GetBool(offlineFlag)
}

// Profile returns the active profile name: --profile when passed,
// else $APS_PROFILE (viper env binding), else "". nil-safe.
func (r *Root) Profile() string {
	if r == nil || r.Viper == nil {
		return ""
	}
	return r.Viper.GetString(profileFlag)
}

// Instance returns the backend-instance name from --instance, or "".
// nil-safe.
func (r *Root) Instance() string {
	if r == nil || r.Viper == nil {
		return ""
	}
	return r.Viper.GetString(instanceFlag)
}

// installNetGlobalsHook returns a PersistentPreRunE hook that stamps
// the resolved --offline/--profile/--instance values onto the command
// context before RunE dispatch. Composes into the kit chain in
// cli.New. Values left at their zero default add no tag, so untouched
// contexts stay clean.
func (r *Root) installNetGlobalsHook() func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		if r.Offline() {
			ctx = WithOffline(ctx, true)
		}
		if p := r.Profile(); p != "" {
			ctx = WithProfile(ctx, p)
		}
		if i := r.Instance(); i != "" {
			ctx = WithInstance(ctx, i)
		}
		cmd.SetContext(ctx)
		return nil
	}
}
