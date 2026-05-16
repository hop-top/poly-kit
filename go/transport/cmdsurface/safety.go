package cmdsurface

import (
	"strings"

	"github.com/spf13/cobra"
)

// Cobra annotation keys the bridge reads. These match the canonical
// kit/ vocabulary registered by go/console/cli (kit/side-effect,
// kit/auth-required, kit/exit-codes, kit/args, kit/idempotent,
// kit/permissions, kit/requires-confirmation).
const (
	annSideEffect      = "kit/side-effect"
	annAuthRequired    = "kit/auth-required"
	annExitCodes       = "kit/exit-codes"
	annArgs            = "kit/args"
	annIdempotent      = "kit/idempotent"
	annPermissions     = "kit/permissions"
	annRequiresConfirm = "kit/requires-confirmation"
)

// SafetyClass captures the bridge's read of a leaf's safety
// annotations. It is the input the policy gate consults to decide
// whether a given Surface may invoke the leaf.
type SafetyClass struct {
	// Destructive is true when kit/side-effect is one of the
	// destructive tiers (destructive, destructive-local,
	// destructive-shared).
	Destructive bool
	// AuthRequired is true when kit/auth-required is "true".
	AuthRequired bool
	// RequiresConfirmation is true when kit/requires-confirmation
	// is "true".
	RequiresConfirmation bool
	// Permissions is the parsed kit/permissions annotation
	// (comma-separated scope names). Empty when unset.
	Permissions []string
	// ExitCodes is the parsed kit/exit-codes annotation
	// (comma-separated symbols). Empty when unset.
	ExitCodes []string
}

// Classify reads cmd's annotations and returns the bridge-side
// SafetyClass. A nil cmd or nil Annotations yields a zero-value
// class (treated as a read-only, no-auth command).
func Classify(cmd *cobra.Command) SafetyClass {
	var cls SafetyClass
	if cmd == nil || cmd.Annotations == nil {
		return cls
	}
	switch cmd.Annotations[annSideEffect] {
	case "destructive", "destructive-local", "destructive-shared":
		cls.Destructive = true
	}
	if cmd.Annotations[annAuthRequired] == "true" {
		cls.AuthRequired = true
	}
	if cmd.Annotations[annRequiresConfirm] == "true" {
		cls.RequiresConfirmation = true
	}
	cls.Permissions = splitCSV(cmd.Annotations[annPermissions])
	cls.ExitCodes = splitCSV(cmd.Annotations[annExitCodes])
	return cls
}

// splitCSV parses a comma-separated annotation value, trimming
// whitespace and dropping empty entries. Returns nil for the empty
// string so a missing annotation is distinguishable from an
// explicit empty list.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Policy gates which Surface may invoke a leaf with a given
// SafetyClass. The default Policy (zero value) is permissive only
// on the local-runtime surfaces — destructive commands are confined
// to SurfaceCLI and SurfaceLib unless AllowDestructiveOn names
// additional surfaces.
type Policy struct {
	// AllowDestructiveOn lists surfaces on which destructive leaves
	// MAY be invoked. SurfaceCLI and SurfaceLib are always allowed
	// regardless of this slice's contents. Empty slice = "block all
	// remote destructive invocations".
	AllowDestructiveOn []Surface
	// DefaultEnabled lists surfaces a leaf is exposed on when its
	// per-command config omits the enabled field. Empty = the
	// bridge falls back to [SurfaceCLI, SurfaceLib, SurfaceMCP].
	DefaultEnabled []Surface
}

// DefaultPolicy returns the conservative default policy: no remote
// surfaces may invoke destructive commands; default enablement is
// CLI + Lib + MCP (the surfaces that already work today).
func DefaultPolicy() Policy {
	return Policy{
		DefaultEnabled: []Surface{SurfaceCLI, SurfaceLib, SurfaceMCP},
	}
}

// Allowed reports whether the given SafetyClass may be invoked via
// surface s under p. The rules:
//
//  1. SurfaceCLI and SurfaceLib are always allowed (local runtime).
//  2. Non-destructive commands are allowed on every other surface.
//  3. Destructive commands are allowed only when s is in
//     p.AllowDestructiveOn.
//
// Note: surface enablement (per-leaf opt-in) is gated separately by
// Bridge.Expose. Allowed only enforces the destructive ceiling.
func (p Policy) Allowed(cls SafetyClass, s Surface) bool {
	if s == SurfaceCLI || s == SurfaceLib {
		return true
	}
	if !cls.Destructive {
		return true
	}
	for _, allowed := range p.AllowDestructiveOn {
		if allowed == s {
			return true
		}
	}
	return false
}

// resolvedDefaults returns p.DefaultEnabled or the package-wide
// fallback when unset.
func (p Policy) resolvedDefaults() []Surface {
	if len(p.DefaultEnabled) > 0 {
		return p.DefaultEnabled
	}
	return []Surface{SurfaceCLI, SurfaceLib, SurfaceMCP}
}
