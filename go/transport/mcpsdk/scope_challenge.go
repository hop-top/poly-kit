package mcpsdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// scopePeekLimit bounds how much of a request body the scope challenge
// reads when the surface's own body cap is off: a body longer than the
// cap is passed on unread, and the SDK answers it as it would anyway.
const scopePeekLimit = api.DefaultMaxBodyBytes

// scopeChallenge answers, at the HTTP layer, a tools/call whose bearer
// token lacks a scope the tool declares under kit/permissions: 403 with
// the RFC 6750 §3.1 challenge the MCP authorization spec asks for —
// error="insufficient_scope", the scopes the tool needs, and, behind a
// protected resource, resource_metadata naming its metadata document —
// so an MCP client can step up its authorization and retry.
//
// The refusal needs the HTTP response, which the SDK owns once it reads
// the call, so the check runs before the SDK: it decodes the request,
// builds the call's provenance as the tool handler does, and asks the
// bridge's scope check ([cmdsurface.Bridge.ScopeRefusal]), which audits
// the refusal. Everything else passes on untouched and meets every gate
// in the tool handler, in order: a request that is not a single
// tools/call, a tool the surface does not serve, a caller who presented
// no bearer token or whom nothing verified, arguments that do not
// decode, a call an earlier gate refuses. Those refusals, and every
// other tool-level refusal (permission_denied, rate_limited, ...), stay
// isError tool results: a new token cannot lift them.
func (s *Surface) scopeChallenge(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !api.BearerPresented(r) || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		limit := s.cfg.maxBody
		if limit <= 0 {
			limit = scopePeekLimit
		}
		head, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		r.Body = readCloser{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}
		if err != nil || int64(len(head)) > limit {
			next.ServeHTTP(w, r)
			return
		}
		if id, refusal := s.scopeRefusal(r, head); refusal != nil {
			writeScopeRefusal(w, r, id, refusal)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// scopeRefusal returns the scope check's refusal of the tools/call in
// body, with the call's JSON-RPC id, or nil.
func (s *Surface) scopeRefusal(r *http.Request, body []byte) (json.RawMessage, error) {
	var msg struct {
		ID     json.RawMessage        `json:"id"`
		Method string                 `json:"method"`
		Params *mcp.CallToolParamsRaw `json:"params"`
	}
	if json.Unmarshal(body, &msg) != nil || msg.Method != "tools/call" || msg.Params == nil {
		return nil, nil
	}
	leaf := s.servedLeaf(msg.Params.Name)
	if leaf == nil {
		return nil, nil
	}
	ctx := r.Context()
	req := &mcp.CallToolRequest{Params: msg.Params, Extra: &mcp.RequestExtra{Header: r.Header}}
	meta := s.callMeta(ctx, req)
	s.authenticated(ctx, req, &meta)
	if meta.Established != cmdsurface.EstablishedVerified {
		return nil, nil
	}
	flags, args, err := decodeArguments(leaf, msg.Params.Arguments)
	if err != nil {
		return nil, nil
	}
	inv := cmdsurface.Invocation{Path: leaf.Path, Flags: flags, Args: args, Meta: meta}
	if err := s.b.ScopeRefusal(ctx, inv); errors.Is(err, cmdsurface.ErrInsufficientScope) {
		return msg.ID, err
	}
	return nil, nil
}

// servedLeaf returns the leaf the surface serves as tool name, or nil.
func (s *Surface) servedLeaf(name string) *cmdsurface.Leaf {
	s.mu.Lock()
	served := s.registered[name]
	s.mu.Unlock()
	if !served {
		return nil
	}
	for _, leaf := range s.b.Leaves() {
		if toolName(leaf.Path) == name {
			return leaf
		}
	}
	return nil
}

// writeScopeRefusal answers the call 403 insufficient_scope: the
// challenge in WWW-Authenticate, and a JSON-RPC error for the call's id
// whose message leads with the code and whose data carries it and the
// scopes, as [RefuseJSONRPC] shapes an HTTP-plane refusal.
func writeScopeRefusal(w http.ResponseWriter, r *http.Request, id json.RawMessage, err error) {
	scopes, _ := cmdsurface.RequiredScopes(err)
	w.Header().Set("WWW-Authenticate", api.ScopeChallenge(r, scopes))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32600,
			"message": api.CodeInsufficientScope + ": " + err.Error(),
			"data":    map[string]any{"code": api.CodeInsufficientScope, "scopes": scopes},
		},
	})
}
