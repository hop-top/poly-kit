package cmdsurface

// MRTR (multi round-trip requests) confirmation for the modern
// (2026-07-28) tools/call path: the elicitation-based strategy that
// fills the confirmation-gate slot when a mount is given key material
// via WithMCPConfirmationKey.
//
// Flow (spec: basic/patterns/mrtr + client/elicitation): the first
// call on a kit/requires-confirmation leaf returns
// resultType "input_required" carrying a single elicitation/create
// form request under the reserved inputRequests key "confirm" plus an
// integrity-protected requestState. The client gathers the user's
// decision and retries the original call (new JSON-RPC id) with
// params.inputResponses and the echoed params.requestState; the gate
// verifies the state and lets the invocation proceed only when the
// confirm response's action is "accept". Decline/cancel refuse the
// call with an isError complete result.
//
// Statelessness: everything a retry needs lives inside requestState.
// There is no server-side pending-request storage of any kind, so any
// instance holding the same key verifies state minted by any other.
// The corollary (spec: replay warning) is that an accepted state
// remains honorable for identical (leaf, arguments, principal) until
// its TTL lapses — single-use redemption would require server-side
// state this surface deliberately does not keep; the short TTL bounds
// the window.
//
// Two verification-failure cases are deliberately distinct (ADR
// 0004): an expired-but-authentic state is a routine re-prompt; a
// state failing HMAC verification is never honored — the rejection is
// recorded as a security-relevant audit event first, and only then is
// a fresh prompt (with newly minted state) issued. Tampering can
// therefore cause nothing worse than request failure, but is never
// silently treated as a re-prompt.
//
// MRTR confirmation is an alternative way to SATISFY confirmation,
// never a way to bypass the destructive ceiling: Policy.Allowed runs
// inside Bridge.Invoke after this gate, exactly as on every other
// path.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// mcpConfirmInputRequestKey is the single reserved inputRequests key
// the confirmation flow uses; the retry's answer is read from the
// same key in params.inputResponses.
const mcpConfirmInputRequestKey = "confirm"

// mcpConfirmStateTTL is the lifetime of a minted requestState. Long
// enough for a human to read and answer the prompt; short enough to
// bound the replay window the stateless design cannot otherwise close
// (see the file comment).
const mcpConfirmStateTTL = 5 * time.Minute

// mcpConfirmStateVersion tags the requestState wire format so a
// future format change invalidates (rather than misparses) old state.
const mcpConfirmStateVersion = "v1"

// errMCPConfirmStateRejected is the sentinel carried on the audit
// event emitted when a presented requestState fails integrity
// verification.
var errMCPConfirmStateRejected = errors.New("cmdsurface: mcp confirmation requestState failed verification")

// mcpConfirmBinding is the request context a requestState is bound
// to. The HMAC covers every field plus the expiry, so state presented
// for a different leaf, different arguments, or by a different
// principal fails verification outright (spec: reject state presented
// on a request that does not match / by a different principal).
type mcpConfirmBinding struct {
	// tool is the leaf path key ("widget purge").
	tool string
	// argsDigest is the hex SHA-256 of the canonically serialized
	// params.arguments (see mcpConfirmArgsDigest).
	argsDigest string
	// principal is the hex SHA-256 of the Authorization header value,
	// or "" when the request carried none. Hashed so the derived MAC
	// input never embeds credential material.
	principal string
}

// elicitationConfirmGate is the mcpConfirmationGate strategy for
// mounts configured with WithMCPConfirmationKey. Clients that did not
// declare form-mode elicitation support keep the X-Confirm-Token
// header gate — the spec forbids sending inputRequests for
// capabilities the client has not declared, and the capability stays
// optional precisely because this fallback exists (never -32021 for
// confirmation).
func (h *mcpModernHandler) elicitationConfirmGate(req *http.Request, leaf *Leaf, rpc jsonRPCRequest) (map[string]any, int) {
	if !leaf.Class.RequiresConfirmation {
		return nil, 0
	}
	if !mcpClientSupportsFormElicitation(rpc.Params) {
		return mcpHeaderConfirmationGate(req, leaf, rpc)
	}
	binding := mcpConfirmBinding{
		tool:       leaf.PathKey(),
		argsDigest: mcpConfirmArgsDigest(rpc.Params),
		principal:  mcpConfirmPrincipal(req),
	}
	retry := parseMCPConfirmRetry(rpc.Params)
	if retry.state == "" {
		// First call — or a retry without the state it was required
		// to echo, which is indistinguishable from one and equally
		// unverifiable: prompt (again).
		return h.confirmInputRequired(leaf, binding), http.StatusOK
	}
	switch verifyMCPConfirmState(h.cfg.confirmKey, retry.state, binding, time.Now()) {
	case mcpConfirmStateInvalid:
		// Tampered, malformed, or presented for a different request /
		// principal than it was minted for: never honored, audited,
		// then re-prompted with fresh state (see file comment).
		h.auditConfirmStateRejected(req, leaf)
		return h.confirmInputRequired(leaf, binding), http.StatusOK
	case mcpConfirmStateExpired:
		// Authentic but past its TTL: routine re-prompt (spec:
		// re-request missing information rather than error), no audit
		// event.
		return h.confirmInputRequired(leaf, binding), http.StatusOK
	}
	switch retry.action {
	case "accept":
		return nil, 0
	case "decline", "cancel":
		return errorResultBlock("confirmation declined"), http.StatusOK
	default:
		// The confirm answer is missing or unusable (absent
		// inputResponses, non-object entry, unknown action): the
		// requested information was not provided, so re-request it
		// rather than erroring (spec SHOULD).
		return h.confirmInputRequired(leaf, binding), http.StatusOK
	}
}

// confirmInputRequired builds the input_required result envelope for
// one confirmation prompt: the reserved "confirm" elicitation/create
// form request plus freshly minted requestState (satisfying the spec
// MUST of carrying at least one of inputRequests / requestState —
// this flow always carries both). The caller stamps the envelope
// (_meta serverInfo; resultType stays "input_required") and writes it
// at HTTP 200. Interim input_required results are never cacheable: no
// ttlMs / cacheScope members, ever.
func (h *mcpModernHandler) confirmInputRequired(leaf *Leaf, binding mcpConfirmBinding) map[string]any {
	return map[string]any{
		"resultType": mcpResultTypeInputRequired,
		"inputRequests": map[string]any{
			mcpConfirmInputRequestKey: map[string]any{
				"method": "elicitation/create",
				"params": map[string]any{
					"mode":    "form",
					"message": fmt.Sprintf("Approve execution of %q?", toolName(leaf.Path)),
					// No form fields: the approval rides the elicit
					// action (accept / decline / cancel), so the
					// requested schema is the empty object.
					"requestedSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
					},
				},
			},
		},
		"requestState": mintMCPConfirmState(h.cfg.confirmKey, binding, time.Now().Add(mcpConfirmStateTTL)),
	}
}

// mcpClientSupportsFormElicitation reports whether the request's
// io.modelcontextprotocol/clientCapabilities declares form-mode
// elicitation. Per spec, an empty elicitation object ({}) declares
// form-only support; a non-empty object must name "form" among its
// modes (a url-only client cannot receive this flow's form request).
// Anything that is not a conforming object declaration — key absent,
// value null or non-object — counts as undeclared, failing toward the
// header fallback rather than toward sending a request the client
// never said it could handle.
func mcpClientSupportsFormElicitation(rawParams json.RawMessage) bool {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(rawParams, &p); err != nil || p.Meta == nil {
		return false
	}
	capsRaw, ok := p.Meta[metaKeyClientCapabilities]
	if !ok {
		return false
	}
	var caps map[string]json.RawMessage
	if err := json.Unmarshal(capsRaw, &caps); err != nil || caps == nil {
		return false
	}
	modesRaw, ok := caps["elicitation"]
	if !ok {
		return false
	}
	var modes map[string]json.RawMessage
	if err := json.Unmarshal(modesRaw, &modes); err != nil || modes == nil {
		return false
	}
	if len(modes) == 0 {
		return true
	}
	_, hasForm := modes["form"]
	return hasForm
}

// mcpConfirmRetry is the tolerant read of the MRTR retry members of a
// tools/call request. Members that are absent or of the wrong JSON
// type simply stay zero — the gate treats a missing state as "prompt"
// and a missing/unusable action as "re-prompt", so malformed retries
// converge on a fresh input_required rather than a decode error.
// These fields are read here, not on the shared callParams struct:
// widening that struct would make the byte-frozen legacy handler
// reject legacy requests that happen to carry same-named members of
// other types.
type mcpConfirmRetry struct {
	state  string
	action string
}

// parseMCPConfirmRetry extracts params.requestState and the
// params.inputResponses "confirm" entry's action from raw params.
func parseMCPConfirmRetry(rawParams json.RawMessage) mcpConfirmRetry {
	var p struct {
		RequestState   json.RawMessage            `json:"requestState"`
		InputResponses map[string]json.RawMessage `json:"inputResponses"`
	}
	_ = json.Unmarshal(rawParams, &p)
	var out mcpConfirmRetry
	if len(p.RequestState) > 0 {
		_ = json.Unmarshal(p.RequestState, &out.state)
	}
	if raw, ok := p.InputResponses[mcpConfirmInputRequestKey]; ok {
		var er struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(raw, &er)
		out.action = er.Action
	}
	return out
}

// mcpConfirmPrincipal derives the principal component of the state
// binding from the request: hex SHA-256 of the Authorization value,
// "" when the header is absent. Presence-only bearer checking is all
// this surface does for auth (Class.AuthRequired), so the raw header
// value is the closest stable principal identifier available; hashing
// keeps credential material out of the MAC input.
func mcpConfirmPrincipal(req *http.Request) string {
	auth := req.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(auth))
	return hex.EncodeToString(sum[:])
}

// mcpConfirmArgsDigest returns the hex SHA-256 of the canonically
// serialized params.arguments. Canonical form is encoding/json's
// map re-marshal, which sorts object keys at every nesting level, so
// equal argument sets digest identically regardless of the client's
// key order; absent arguments canonicalize to "null". Only the
// arguments participate: _meta, inputResponses, and requestState all
// legitimately differ between the first call and its retry, and the
// tool name is bound separately via the leaf path key.
func mcpConfirmArgsDigest(rawParams json.RawMessage) string {
	var p struct {
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal(rawParams, &p)
	canonical, err := json.Marshal(p.Arguments)
	if err != nil {
		// Unreachable for JSON-decoded values; fail closed to the
		// absent-arguments form rather than panicking.
		canonical = []byte("null")
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// mcpConfirmStateMAC computes the HMAC-SHA-256 tag binding a state to
// its expiry and request context. Each component is written
// length-prefixed so the concatenation is unambiguous whatever the
// component contents (no delimiter-injection ambiguity), with a
// leading domain-separation constant so the tag can never be confused
// with any other HMAC this package mints under a shared key.
func mcpConfirmStateMAC(key []byte, b mcpConfirmBinding, exp int64) []byte {
	mac := hmac.New(sha256.New, key)
	for _, part := range []string{
		"cmdsurface-mcp-confirm-" + mcpConfirmStateVersion,
		strconv.FormatInt(exp, 10),
		b.tool,
		b.argsDigest,
		b.principal,
	} {
		fmt.Fprintf(mac, "%d:%s", len(part), part)
	}
	return mac.Sum(nil)
}

// mintMCPConfirmState renders an opaque-to-clients requestState:
// "v1.<expiry-unix>.<base64url(mac)>". Only the version and expiry
// travel in the clear; the full binding is reconstructed from the
// retry request itself at verification time, which is what keeps the
// state small and the flow stateless.
func mintMCPConfirmState(key []byte, b mcpConfirmBinding, exp time.Time) string {
	e := exp.Unix()
	return mcpConfirmStateVersion + "." + strconv.FormatInt(e, 10) + "." +
		base64.RawURLEncoding.EncodeToString(mcpConfirmStateMAC(key, b, e))
}

// mcpConfirmStateStatus is the outcome of verifying a presented
// requestState.
type mcpConfirmStateStatus int

const (
	mcpConfirmStateValid mcpConfirmStateStatus = iota
	mcpConfirmStateExpired
	mcpConfirmStateInvalid
)

// verifyMCPConfirmState checks a presented state against the current
// request's binding. Authenticity is decided before expiry, so
// "expired" is only ever reported for a state that verifiably came
// from this key and this exact binding — a tampered expiry fails the
// MAC and lands in Invalid, never in Expired. Any structural defect
// (wrong part count, unknown version, non-decimal expiry, undecodable
// tag) is a verification failure too: a state that cannot be verified
// is never honored.
func verifyMCPConfirmState(key []byte, state string, b mcpConfirmBinding, now time.Time) mcpConfirmStateStatus {
	parts := strings.Split(state, ".")
	if len(parts) != 3 || parts[0] != mcpConfirmStateVersion {
		return mcpConfirmStateInvalid
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return mcpConfirmStateInvalid
	}
	// Strict() rejects a non-canonical encoding. The tag is 32 bytes,
	// so its 43-character encoding ends on a character carrying 4 data
	// bits plus 2 unused ones. A non-strict decoder discards those two,
	// which makes 4 distinct final characters decode to the same tag —
	// an attacker who flips one of them would present a state that is
	// not the one we minted and still pass hmac.Equal. Canonical-only
	// decoding is what makes "the bytes differ" and "the MAC differs"
	// the same statement.
	tag, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		return mcpConfirmStateInvalid
	}
	if !hmac.Equal(tag, mcpConfirmStateMAC(key, b, exp)) {
		return mcpConfirmStateInvalid
	}
	if time.Unix(exp, 0).Before(now) {
		return mcpConfirmStateExpired
	}
	return mcpConfirmStateValid
}

// auditConfirmStateRejected records a failed requestState
// verification as a security-relevant audit event on the bridge's
// registered sinks (OnError specs match it; Surfaces/Paths filters
// apply as usual). Registry sinks are the bridge-side audit fan-out;
// runner-wrapping sinks never observe pre-flight refusals on any
// surface, this one included. Best-effort by the Sink contract —
// emission failures never affect the response.
func (h *mcpModernHandler) auditConfirmStateRejected(req *http.Request, leaf *Leaf) {
	inv := Invocation{
		Path: append([]string(nil), leaf.Path...),
		Meta: Meta{
			Surface:     SurfaceMCP,
			RequestedAt: time.Now(),
			Extra: map[string]string{
				"mcp_spec_version":      mcpModernProtocolVersion,
				"mcp_confirm_rejection": "request_state_verification_failed",
			},
		},
	}
	_ = h.b.Sinks().Emit(req.Context(), inv, Result{}, errMCPConfirmStateRejected)
}
