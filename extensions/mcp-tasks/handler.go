// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package tasks

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// taskParams is the wire params object shared by the three tasks/*
// methods. Embedding mcp.ParamsBase satisfies the SDK's Params
// interface, which carries the per-request "_meta" the extension's
// capability check reads.
type taskParams struct {
	mcp.ParamsBase
	TaskID         string               `json:"taskId"`
	InputResponses mcp.InputResponseMap `json:"inputResponses,omitempty"`
}

// headerContextKey carries the originating request's HTTP headers from
// the receiving middleware to the tasks/* handlers. The SDK hands
// custom-method handlers only (ctx, session, params) — RequestExtra,
// and with it the header the principal is derived from, is reachable
// only from middleware, which sees the full mcp.Request.
type headerContextKey struct{}

// registerMethods installs the extension's three methods on s through
// the SDK's own custom-method table, so every tasks/* request is
// dispatched by the SDK's streamable HTTP handler and inherits its
// full transport posture: DNS-rebinding (Host) protection, cross-origin
// checks, the MaxRequestBodyBytes cap applied during the read,
// Content-Type and Accept negotiation, protocol-version validation,
// session state, and the SEP-2243 Mcp-Method header check. The
// extension adds no HTTP handler of its own, so there is no path to
// task state that skips any of it.
//
// tasks/list and tasks/result are deliberately left unregistered: the
// SDK answers unknown methods with the -32601 SEP-2663 mandates for
// them, and the "tasks/" prefix stays reserved to this extension.
func (e *Extension) registerMethods(s *mcp.Server) error {
	register := func(method string, fn func(context.Context, *taskParams, http.Header) (mcp.Result, error)) error {
		return mcp.AddReceivingCustomMethod(s, method,
			func(ctx context.Context, _ *mcp.ServerSession, p *taskParams) (mcp.Result, error) {
				hdr, _ := ctx.Value(headerContextKey{}).(http.Header)
				if p == nil {
					p = &taskParams{}
				}
				if jerr := e.checkRequest(p, hdr, method); jerr != nil {
					return nil, jerr
				}
				return fn(ctx, p, hdr)
			})
	}
	if err := register(MethodGet, e.handleGet); err != nil {
		return err
	}
	if err := register(MethodUpdate, e.handleUpdate); err != nil {
		return err
	}
	return register(MethodCancel, e.handleCancel)
}

// checkRequest applies the guards every tasks/* method shares: the
// SEP-2243 Mcp-Name agreement, the per-request capability declaration
// SEP-2663 makes a MUST, and the presence of a task ID.
func (e *Extension) checkRequest(p *taskParams, hdr http.Header, method string) *jsonrpc.Error {
	if jerr := validateNameHeader(hdr, method, p.TaskID); jerr != nil {
		return jerr
	}
	// Per-request capability declaration is a spec MUST for tasks/*:
	// non-declaring clients get -32003 with the required extension.
	if !declaresInMeta(p.GetMeta()) {
		return MissingClientCapabilityError()
	}
	if p.TaskID == "" {
		return &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
			Message: "invalid params: taskId required",
		}
	}
	return nil
}

// minVersionForRoutingHeaders is the protocol version that made
// Mcp-Method and Mcp-Name mandatory (SEP-2243). Below it — including
// when Mcp-Protocol-Version is absent — the headers are not validated,
// matching the SDK's own treatment of pre-2026-07-28 clients.
const minVersionForRoutingHeaders = "2026-07-28"

// validateNameHeader enforces the SEP-2663 routing requirement that
// Mcp-Name mirrors params.taskId, so intermediaries can route polls to
// the instance holding the task.
//
// The companion Mcp-Method check is not repeated here: the SDK applies
// it to every request it dispatches, custom methods included, with the
// same -32020 code and the same pre-2026-07-28 tolerance. Mcp-Name is
// the half the SDK validates only for tools/call, resources/read and
// prompts/get, since only those have a name it knows how to extract —
// so the extension supplies it for its own methods.
func validateNameHeader(hdr http.Header, method, taskID string) *jsonrpc.Error {
	if hdr == nil {
		return nil
	}
	if v := hdr.Get("Mcp-Protocol-Version"); v == "" || v < minVersionForRoutingHeaders {
		return nil
	}
	switch n := hdr.Get("Mcp-Name"); {
	case n == "":
		return headerMismatchError(fmt.Sprintf("missing required Mcp-Name header for method %q", method))
	case n != taskID:
		return headerMismatchError(fmt.Sprintf(
			"header mismatch: Mcp-Name header value %q does not match body value %q", n, taskID))
	}
	return nil
}

// headerMismatchError builds the SEP-2243 HeaderMismatch error.
func headerMismatchError(msg string) *jsonrpc.Error {
	return &jsonrpc.Error{Code: CodeHeaderMismatch, Message: msg}
}

// lookup resolves the task a request names, authorized against the
// creating principal. An unknown ID, an expired-and-pruned task, and
// another principal's task all produce the identical error: there must
// be no oracle revealing that a foreign task exists.
func (e *Extension) lookup(ctx context.Context, taskID string, hdr http.Header) (*Record, *jsonrpc.Error) {
	rec, err := e.store.Get(ctx, taskID)
	if err != nil || rec.Principal != e.principalOf(hdr) {
		return nil, taskNotFoundError()
	}
	return rec, nil
}

// handleGet serves tasks/get.
func (e *Extension) handleGet(ctx context.Context, p *taskParams, hdr http.Header) (mcp.Result, error) {
	rec, jerr := e.lookup(ctx, p.TaskID, hdr)
	if jerr != nil {
		return nil, jerr
	}
	return &detailedTaskResult{ResultType: "complete", Task: rec.Task}, nil
}

// handleUpdate serves tasks/update.
func (e *Extension) handleUpdate(ctx context.Context, p *taskParams, hdr http.Header) (mcp.Result, error) {
	if _, jerr := e.lookup(ctx, p.TaskID, hdr); jerr != nil {
		return nil, jerr
	}
	e.deliver(ctx, p.TaskID, p.InputResponses)
	return &emptyAckResult{ResultType: "complete"}, nil
}

// handleCancel serves tasks/cancel.
func (e *Extension) handleCancel(ctx context.Context, p *taskParams, hdr http.Header) (mcp.Result, error) {
	if _, jerr := e.lookup(ctx, p.TaskID, hdr); jerr != nil {
		return nil, jerr
	}
	e.cancelTask(p.TaskID)
	return &emptyAckResult{ResultType: "complete"}, nil
}

// declaresInMeta reports whether the request's _meta carries the
// per-request client capability declaration for the tasks extension.
func declaresInMeta(meta map[string]any) bool {
	raw, ok := meta[mcp.MetaKeyClientCapabilities]
	if !ok {
		return false
	}
	caps, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	exts, ok := caps["extensions"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = exts[ExtensionID]
	return ok
}

// taskNotFoundError is the single -32602 shape for every unknown,
// expired, or foreign task ID.
func taskNotFoundError() *jsonrpc.Error {
	return &jsonrpc.Error{
		Code:    jsonrpc.CodeInvalidParams,
		Message: "failed to retrieve task: task not found",
	}
}
