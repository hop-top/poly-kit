package cmdsurface

import (
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/cmdreflect"
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
//
// Deprecated: Classify reflects a single command in isolation.
// Reflect the whole tree with [hop.top/kit/go/ai/cmdreflect.Reflect]
// and read Leaf.Descriptor instead — one reflection, and commands
// the bridge excludes carry a reason rather than vanishing. Classify
// remains as a thin shim over the same resolution so existing
// callers keep working.
func Classify(cmd *cobra.Command) SafetyClass {
	if cmd == nil {
		return SafetyClass{}
	}
	tree := cmdreflect.Reflect(cmd)
	if tree.Root == nil {
		return SafetyClass{}
	}
	return classFromDescriptor(tree.Root)
}

// classFromDescriptor projects a reflected descriptor into the
// bridge's SafetyClass. The class is a narrow view of
// cmdreflect.Safety — just the fields the policy gate consults —
// kept as its own type because it is the bridge's public contract.
func classFromDescriptor(d *cmdreflect.Descriptor) SafetyClass {
	if d == nil {
		return SafetyClass{}
	}
	return SafetyClass{
		Destructive:  d.Safety.Destructive(),
		AuthRequired: d.Safety.AuthRequired,
		// The bridge's confirmation axis is the DECLARED
		// annotation, not the resolved verdict: destructiveness
		// is already gated separately by Policy.Allowed, and
		// folding it in here would demand a confirm token for
		// every destructive leaf on every surface.
		RequiresConfirmation: d.Safety.ConfirmationDeclared,
		Permissions:          splitCSV(annotationOf(d.Cmd, annPermissions)),
		ExitCodes:            d.Safety.ExitCodes,
	}
}

// annotationOf reads one annotation off a cobra command, tolerating
// a nil command or nil map.
func annotationOf(cmd *cobra.Command, key string) string {
	if cmd == nil || cmd.Annotations == nil {
		return ""
	}
	return cmd.Annotations[key]
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
