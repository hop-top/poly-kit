package mcpsdk

// Per-call gates kit applies before a tool call reaches the bridge:
// provenance, authentication for kit/auth-required leaves, and the
// confirmation gate for kit/requires-confirmation leaves. The
// defaults read HTTP headers, which is what a bare Mount has; the
// options let a host that knows more about its transport — a verified
// identity, a spawned stdio peer, a client able to ask its user —
// answer instead.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
)

// confirmHeader is the request header that satisfies the confirmation
// gate over HTTP, as on every kit MCP surface.
const confirmHeader = "X-Confirm-Token"

// elicitConfirmTTL is the lifetime of a confirmation requestState on
// the synchronous path: long enough for a person to read and answer
// the question, short enough to bound the window in which an accepted
// state stays redeemable for the same call.
const elicitConfirmTTL = 5 * time.Minute

// Sentinels carried on the audit records of the gates in this file.
// The bridge's own sinks receive them through [cmdsurface.Bridge.Audit].
var (
	// ErrConfirmationRequired records a kit/requires-confirmation call
	// refused because nothing satisfied the confirmation gate.
	ErrConfirmationRequired = errors.New("mcpsdk: confirmation required")
	// ErrConfirmationDeclined records a call whose confirmation
	// question the client's user declined or dismissed.
	ErrConfirmationDeclined = errors.New("mcpsdk: confirmation declined")
	// ErrConfirmationStateRejected records a presented requestState
	// that failed verification. It is never honored; the question is
	// asked again.
	ErrConfirmationStateRejected = errors.New("mcpsdk: confirmation requestState failed verification")
)

// WithCallMeta installs fn to supply each tool call's provenance: the
// [cmdsurface.Meta] the bridge's permission gate and audit sinks see.
// Surface is always pinned to [cmdsurface.SurfaceMCP] and RequestedAt
// is stamped when fn leaves it zero. Without it a call carries only
// those two fields.
//
// fn fills Caller and Tenant only from an identity the host verified,
// and sets Established to say how it was established (a verifier's
// verdict, or the transport's own proof such as a stdio spawn); a
// claim the client made is provenance for Extra, not a principal.
// Without WithAuthenticated, the Established fn returns is what the
// kit/auth-required gate reads.
func WithCallMeta(fn func(ctx context.Context, req *mcp.CallToolRequest) cmdsurface.Meta) Option {
	return func(c *config) { c.callMeta = fn }
}

// WithAuthenticated installs the predicate the gate for
// kit/auth-required leaves asks: whether the caller of req is
// authenticated. A call it accepts runs as
// [cmdsurface.EstablishedVerified] unless [WithCallMeta] already
// established it; one it refuses runs unestablished.
//
// Without it the gate reads the Meta [WithCallMeta] returned: only an
// established one is authenticated. An Authorization header is
// presence, not verification, so a bare Mount refuses every
// kit/auth-required leaf until the host says who verified the call.
func WithAuthenticated(fn func(ctx context.Context, req *mcp.CallToolRequest) bool) Option {
	return func(c *config) { c.authenticated = fn }
}

// WithConfirmationElicitation lets a person satisfy the confirmation
// gate for kit/requires-confirmation leaves by answering an
// elicitation, in addition to the X-Confirm-Token header.
//
// When the call carries no X-Confirm-Token and the client has declared
// form elicitation, the call is answered with a single question —
// `Approve execution of "<tool>"?`, no form fields — through the SDK's
// multi round-trip machinery: a client on protocol 2026-07-28 receives
// an input_required result and retries with the answer, an older
// client receives elicitation/create on its session and the SDK
// resumes the call. accept runs the call once; decline or cancel
// refuses it. A client offering neither the header nor elicitation is
// refused with a message naming both.
//
// The requestState is signed with key (random per Surface when key is
// empty; share one key across instances behind a load balancer) and
// bound to the tool, the digest of the arguments, and the caller, for
// five minutes. A state that fails verification is audited with
// [ErrConfirmationStateRejected] and the question is asked again.
//
// The answer satisfies only this gate. The destructive ceiling and the
// permission gate still run inside [cmdsurface.Bridge.Invoke], and a
// destructive command's own confirm flag is still the command's.
// Calls diverted onto the tasks path (see [WithTasks]) keep that
// path's own confirmation exchange.
func WithConfirmationElicitation(key []byte) Option {
	return func(c *config) {
		c.elicitConfirm = true
		c.elicitKey = append([]byte(nil), key...)
	}
}

// callMeta returns the provenance for req with the surface pinned.
func (s *Surface) callMeta(ctx context.Context, req *mcp.CallToolRequest) cmdsurface.Meta {
	var meta cmdsurface.Meta
	switch {
	case s.cfg.callMeta != nil:
		meta = s.cfg.callMeta(ctx, req)
	case s.cfg.protected != nil:
		// Past WithProtectedResource's verifier: the caller it
		// accepted.
		meta, _ = verifiedMeta(headerOf(req))
	}
	meta.Surface = cmdsurface.SurfaceMCP
	if meta.RequestedAt.IsZero() {
		meta.RequestedAt = time.Now()
	}
	return meta
}

// authenticated answers the kit/auth-required gate for req and
// records the answer on meta: the installed predicate, or, without
// one, whether the call's Meta is already established.
func (s *Surface) authenticated(ctx context.Context, req *mcp.CallToolRequest, meta *cmdsurface.Meta) bool {
	if s.cfg.authenticated == nil {
		return meta.Authenticated()
	}
	if !s.cfg.authenticated(ctx, req) {
		meta.Established = cmdsurface.EstablishedNone
		return false
	}
	if !meta.Authenticated() {
		meta.Established = cmdsurface.EstablishedVerified
	}
	return true
}

// confirmGate answers the kit/requires-confirmation gate. A nil
// result means the call may proceed; otherwise the result is the
// reply — a refusal, or an input_required round trip.
func (s *Surface) confirmGate(ctx context.Context, req *mcp.CallToolRequest, leaf *cmdsurface.Leaf, inv cmdsurface.Invocation) *mcp.CallToolResult {
	if headerOf(req).Get(confirmHeader) != "" {
		return nil
	}
	if s.confirm == nil {
		s.b.Audit(ctx, inv, cmdsurface.Result{}, ErrConfirmationRequired)
		return errorResult("confirmation required")
	}

	binding := confirmBinding{
		leaf:      leaf.PathKey(),
		principal: confirmPrincipal(req, inv.Meta),
		args:      argsDigest(req.Params.Arguments),
	}
	p := req.Params
	if p.RequestState != "" || len(p.InputResponses) > 0 {
		key, st := s.confirm.verify(p.RequestState, binding)
		switch st {
		case confirmValid:
			er, _ := p.InputResponses[key].(*mcp.ElicitResult)
			switch {
			case er != nil && er.Action == "accept":
				return nil
			case er != nil && (er.Action == "decline" || er.Action == "cancel"):
				s.b.Audit(ctx, inv, cmdsurface.Result{}, ErrConfirmationDeclined)
				return errorResult("confirmation declined")
			}
			// No usable answer under the key the state names: the
			// question was not answered, so it is asked again.
		case confirmInvalid:
			s.b.Audit(ctx, inv, cmdsurface.Result{}, ErrConfirmationStateRejected)
		}
	}

	if !supportsFormElicitation(req) {
		s.b.Audit(ctx, inv, cmdsurface.Result{}, ErrConfirmationRequired)
		return errorResult(fmt.Sprintf(
			"confirmation required: %s requires confirmation; approve it from a client that supports elicitation, or send the %s header over HTTP",
			toolName(leaf.Path), confirmHeader))
	}
	key, state := s.confirm.mint(binding)
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{
			key: &mcp.ElicitParams{
				Mode:    "form",
				Message: fmt.Sprintf("Approve execution of %q?", toolName(leaf.Path)),
				// No form fields: the approval is the elicitation's
				// action, so the requested schema is the empty object.
				RequestedSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		RequestState: state,
	}
}

// confirmPrincipal is the caller a confirmation state is bound to:
// the verified Caller when the host supplied one, else the digest of
// the Authorization header, else empty.
func confirmPrincipal(req *mcp.CallToolRequest, meta cmdsurface.Meta) string {
	if meta.Caller != "" {
		return meta.Caller
	}
	return taskPrincipal(headerOf(req))
}

// supportsFormElicitation reports whether the calling client declared
// form elicitation: per request on protocol 2026-07-28, from the
// session's initialize otherwise. An elicitation capability naming
// only url mode cannot receive a form question.
func supportsFormElicitation(req *mcp.CallToolRequest) bool {
	caps := req.ClientCapabilities()
	if caps == nil || caps.Elicitation == nil {
		return false
	}
	e := caps.Elicitation
	return e.Form != nil || e.URL == nil
}

// headerOf returns the HTTP headers the SDK attached to req, or an
// empty set on a transport without them.
func headerOf(req *mcp.CallToolRequest) http.Header {
	if req == nil || req.Extra == nil || req.Extra.Header == nil {
		return http.Header{}
	}
	return req.Extra.Header
}
