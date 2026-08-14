// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package tasks

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler returns an http.Handler that services the extension's
// methods — tasks/get, tasks/update, tasks/cancel, plus the mandated
// -32601 for the reserved-but-nonexistent tasks/list and tasks/result
// — and passes every other request to next untouched (typically the
// SDK's streamable HTTP handler).
//
// This front-of-the-SDK routing exists because go-sdk v1.7.0 rejects
// methods outside its own table at the transport layer, before any
// server middleware runs; there is no in-SDK seam to route extension
// methods through. The interception is scoped strictly to the
// "tasks/" method prefix, which SEP-2663 reserves for this extension.
//
// Per SEP-2243 clients set Mcp-Method, and on tasks/* they must set
// Mcp-Name to params.taskId so intermediaries can route polls to the
// instance holding the task. The handler tolerates both headers
// (using Mcp-Method only as a fast path that avoids reading bodies of
// unrelated requests) and never rejects on their absence or mismatch.
func (e *Extension) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		if mm := r.Header.Get("Mcp-Method"); mm != "" && !strings.HasPrefix(mm, "tasks/") {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			http.Error(w, "reading request body", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		var env struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &env) != nil || !strings.HasPrefix(env.Method, "tasks/") {
			next.ServeHTTP(w, r)
			return
		}
		e.serveTasksMethod(w, r, env.ID, env.Method, env.Params)
	})
}

// serveTasksMethod handles one JSON-RPC request for a tasks/* method.
func (e *Extension) serveTasksMethod(w http.ResponseWriter, r *http.Request, id json.RawMessage, method string, params json.RawMessage) {
	if len(id) == 0 || string(id) == "null" {
		writeError(w, json.RawMessage("null"), &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidRequest,
			Message: "tasks methods require a request id",
		})
		return
	}

	switch method {
	case MethodGet, MethodUpdate, MethodCancel:
	default:
		// tasks/list and tasks/result deliberately do not exist;
		// SEP-2663 mandates -32601 for them, and the whole tasks/
		// prefix is reserved for this extension.
		writeError(w, id, &jsonrpc.Error{
			Code:    jsonrpc.CodeMethodNotFound,
			Message: "method not found: " + method,
		})
		return
	}

	var p struct {
		Meta           map[string]json.RawMessage `json:"_meta"`
		TaskID         string                     `json:"taskId"`
		InputResponses json.RawMessage            `json:"inputResponses"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			writeError(w, id, &jsonrpc.Error{
				Code:    jsonrpc.CodeInvalidParams,
				Message: "invalid params: " + err.Error(),
			})
			return
		}
	}

	// Per-request capability declaration is a spec MUST for tasks/*:
	// non-declaring clients get -32003 with the required extension.
	if !declaresInMeta(p.Meta) {
		writeError(w, id, MissingClientCapabilityError())
		return
	}

	if p.TaskID == "" {
		writeError(w, id, &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
			Message: "invalid params: taskId required",
		})
		return
	}

	// Authorize against the creating principal. An unknown ID, an
	// expired-and-pruned task, and another principal's task all
	// produce the identical error: there must be no oracle revealing
	// that a foreign task exists.
	rec, err := e.store.Get(r.Context(), p.TaskID)
	if err != nil || rec.Principal != e.principalOf(r.Header) {
		writeError(w, id, taskNotFoundError())
		return
	}

	switch method {
	case MethodGet:
		writeResult(w, id, &detailedTaskResult{ResultType: "complete", Task: rec.Task})

	case MethodUpdate:
		var responses mcp.InputResponseMap
		if len(p.InputResponses) > 0 {
			if err := json.Unmarshal(p.InputResponses, &responses); err != nil {
				writeError(w, id, &jsonrpc.Error{
					Code:    jsonrpc.CodeInvalidParams,
					Message: "invalid params: inputResponses: " + err.Error(),
				})
				return
			}
		}
		e.deliver(r.Context(), p.TaskID, responses)
		writeResult(w, id, &emptyAckResult{ResultType: "complete"})

	case MethodCancel:
		e.cancelTask(p.TaskID)
		writeResult(w, id, &emptyAckResult{ResultType: "complete"})
	}
}

// declaresInMeta reports whether the request's _meta carries the
// per-request client capability declaration for the tasks extension.
func declaresInMeta(meta map[string]json.RawMessage) bool {
	raw, ok := meta[mcp.MetaKeyClientCapabilities]
	if !ok {
		return false
	}
	var caps struct {
		Extensions map[string]json.RawMessage `json:"extensions"`
	}
	if json.Unmarshal(raw, &caps) != nil {
		return false
	}
	_, ok = caps.Extensions[ExtensionID]
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

// writeResult writes a JSON-RPC success envelope.
func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeEnvelope(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// writeError writes a JSON-RPC error envelope.
func writeError(w http.ResponseWriter, id json.RawMessage, jerr *jsonrpc.Error) {
	writeEnvelope(w, map[string]any{"jsonrpc": "2.0", "id": id, "error": jerr})
}

func writeEnvelope(w http.ResponseWriter, env map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(env)
}
