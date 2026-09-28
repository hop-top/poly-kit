package cmdsurface

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"hop.top/kit/go/transport/api"
)

// ErrInsufficientScope is the refusal of the built-in scope check,
// the first decider of the permission gate: the leaf declares
// kit/permissions and the caller's credential lacks at least one of
// those scopes. The error the bridge returns is an
// [*InsufficientScopeError] wrapping it, which names the scopes; read
// the full requirement with [RequiredScopes].
//
// It wraps [ErrPermissionDenied], so a transport that predates the
// class answers it as a permission denial. Transports that know it
// answer insufficient_scope: 403 with an RFC 6750 WWW-Authenticate
// challenge over HTTP, Connect PermissionDenied, an MCP isError
// result, the socket's DENIED code.
var ErrInsufficientScope error = insufficientScope{}

// insufficientScope is [ErrInsufficientScope]'s type: a sentinel of
// its own spelling that still unwraps to [ErrPermissionDenied].
type insufficientScope struct{}

func (insufficientScope) Error() string { return "cmdsurface: insufficient scope" }
func (insufficientScope) Unwrap() error { return ErrPermissionDenied }

// InsufficientScopeError is the scope check's refusal. It wraps
// [ErrInsufficientScope].
type InsufficientScopeError struct {
	// Path is the refused command's path key.
	Path string
	// Surface is the surface the call arrived on.
	Surface Surface
	// Required is every scope the command declares under
	// kit/permissions: what a credential needs to run it.
	Required []string
	// Missing is the part of Required the caller's credential lacks.
	Missing []string
}

// Error implements error.
func (e *InsufficientScopeError) Error() string {
	return fmt.Sprintf("%s: %s on %s: missing scope %s",
		ErrInsufficientScope, e.Path, e.Surface, strings.Join(e.Missing, ", "))
}

// Unwrap makes errors.Is(err, ErrInsufficientScope) hold, and through
// it errors.Is(err, ErrPermissionDenied).
func (e *InsufficientScopeError) Unwrap() error { return ErrInsufficientScope }

// RequiredScopes returns the scopes the refused command requires. It
// is the hint an HTTP surface reads to name them in its
// WWW-Authenticate challenge.
func (e *InsufficientScopeError) RequiredScopes() []string {
	return append([]string(nil), e.Required...)
}

// RequiredScopes reports the scopes a refused command requires, when
// err is a scope refusal (an [*InsufficientScopeError] anywhere in its
// chain).
func RequiredScopes(err error) ([]string, bool) {
	var hint interface{ RequiredScopes() []string }
	if !errors.As(err, &hint) {
		return nil, false
	}
	return hint.RequiredScopes(), true
}

// scopeCheck is the permission gate's first decider (slot 6): a leaf
// declaring kit/permissions runs on a remote surface only for a caller
// whose established identity holds every scope it names. It can only
// narrow; the --policy engine and the adopter's [PermissionFunc] are
// asked after it, and a leaf that declares no permissions is not its
// concern.
//
// Whose scopes count is decided by how the transport established the
// caller ([Meta.Established]):
//
//   - verified: the scopes of the credential the verifier accepted,
//     which the transport carries as Meta.Extra["scopes"].
//   - transport: the owner's authority. The transport proved the
//     caller holds it — the socket's owner-only file, the stdio spawn,
//     the operator's schedule — and whoever holds it can run the
//     command from the CLI, where no scope is asked for. There is no
//     credential to hold a scope, so none is compared.
//   - none: no scopes. A claimed caller, and a scopes entry the
//     client wrote, prove nothing.
//
// The CLI and in-process library surfaces are the operator's own and
// pass, as they pass the authentication gate.
func scopeCheck(meta Meta, leaf *Leaf) error {
	need := leaf.Class.Permissions
	if len(need) == 0 || !meta.Surface.remote() || meta.Established == EstablishedTransport {
		return nil
	}
	have := verifiedScopes(meta)
	var missing []string
	for _, s := range need {
		if !have[s] {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &InsufficientScopeError{
		Path:     leaf.PathKey(),
		Surface:  meta.Surface,
		Required: append([]string(nil), need...),
		Missing:  missing,
	}
}

// verifiedScopes returns the scopes a verified caller's credential
// carries, from Meta.Extra["scopes"]. Anyone else holds none: only a
// verifier's verdict puts scopes there, and a value on an
// unestablished call is a claim.
func verifiedScopes(meta Meta) map[string]bool {
	if meta.Established != EstablishedVerified {
		return nil
	}
	scopes := splitCSV(meta.Extra[scopesExtraKey])
	have := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		have[s] = true
	}
	return have
}

// writeInsufficientScope answers an [ErrInsufficientScope] on an HTTP
// surface: 403 insufficient_scope with the RFC 6750 challenge naming
// the scopes the command requires.
func writeInsufficientScope(w http.ResponseWriter, err error) {
	scopes, _ := RequiredScopes(err)
	api.WriteInsufficientScope(w, scopes, err.Error())
}
