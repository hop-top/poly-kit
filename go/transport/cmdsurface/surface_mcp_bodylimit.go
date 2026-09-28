package cmdsurface

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"hop.top/kit/go/transport/api"
)

// WithMCPMaxBodyBytes caps the JSON-RPC request body at n bytes.
// Zero keeps the default, api.DefaultMaxBodyBytes (1 MiB); a
// negative n disables the cap.
//
// A body over the cap is refused with HTTP 413 and a JSON-RPC error
// (code -32600, data.reason "body_too_large", data.limit the cap)
// before any protocol revision's handler sees it, and the refusal is
// audited through the bridge's sinks as ErrBodyTooLarge.
func WithMCPMaxBodyBytes(n int64) MCPOption {
	return func(c *mcpConfig) { c.maxBody = n }
}

// mcpBodyLimit caps the body of every POST the MCP mount serves.
//
// It reads the body once, through http.MaxBytesReader, and hands the
// delegate a rewound copy: every revision's handler reads the whole
// body anyway, so reading it here costs nothing and keeps the cap in
// one place ahead of the era dispatcher. A read failure that is not
// the cap renders the same -32603 the handlers themselves produce
// for an unreadable body.
func mcpBodyLimit(b *Bridge, limit int64, next http.HandlerFunc) http.HandlerFunc {
	limit = api.MaxBodyBytesOrDefault(limit)
	if limit < 0 {
		return next
	}
	return func(w http.ResponseWriter, req *http.Request) {
		if req.ContentLength > limit {
			refuseMCPBodyTooLarge(w, req, b, limit)
			return
		}
		if req.Body == nil || req.Body == http.NoBody {
			next(w, req)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, limit))
		_ = req.Body.Close()
		if err != nil {
			if l, over := api.AsBodyTooLarge(err); over {
				refuseMCPBodyTooLarge(w, req, b, l)
				return
			}
			writeJSONRPCError(w, nil, mcpErrInternal, "read request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		next(w, req)
	}
}

// refuseMCPBodyTooLarge audits and renders a size-cap refusal. The
// request id is null: the body was never parsed, so there is no id
// to echo, which is what JSON-RPC prescribes for an error detected
// before the id is known.
func refuseMCPBodyTooLarge(w http.ResponseWriter, req *http.Request, b *Bridge, limit int64) {
	b.AuditBodyTooLarge(req, SurfaceMCP, nil, limit)
	writeJSONRPCErrorWithData(w, nil, mcpErrInvalidRequest,
		fmt.Sprintf("request body exceeds %d bytes", limit),
		http.StatusRequestEntityTooLarge,
		map[string]any{"reason": api.CodeBodyTooLarge, "limit": limit})
}
