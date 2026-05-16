package cmdsurface

// We intentionally diverge from go/ai/toolspec/adapters/mcp.go here.
// That adapter is for `<tool> spec --format mcp` (one MCP tool per
// CLI tool, with an "action" enum). This surface is for live MCP exec
// (one MCP tool per leaf command). The type-mapping logic is small
// enough that duplicating it beats coupling.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"hop.top/kit/go/transport/api"
)

// MCP JSON-RPC 2.0 error codes used by this surface.
const (
	mcpErrParse          = -32700
	mcpErrInvalidRequest = -32600
	mcpErrMethodNotFound = -32601
	mcpErrInvalidParams  = -32602
	mcpErrInternal       = -32603
)

// Default identity for the MCP server in initialize responses.
const (
	defaultMCPServerName    = "cmdsurface"
	defaultMCPServerVersion = "0.0.0"
	mcpProtocolVersion      = "2024-11-05"
	defaultMCPPath          = "/mcp"
)

// mcpConfig is the internal options bag set by MCPOption funcs.
type mcpConfig struct {
	path          string
	serverName    string
	serverVersion string
}

// MCPOption configures the MCP surface mounted by MountMCP.
type MCPOption func(*mcpConfig)

// WithMCPPath overrides the default mount path ("/mcp").
func WithMCPPath(path string) MCPOption {
	return func(c *mcpConfig) { c.path = path }
}

// WithMCPServerInfo sets the server identity returned by the
// `initialize` method. Defaults: name="cmdsurface", version="0.0.0".
func WithMCPServerInfo(name, version string) MCPOption {
	return func(c *mcpConfig) {
		c.serverName = name
		c.serverVersion = version
	}
}

// MountMCP mounts an MCP JSON-RPC HTTP handler at the given path on
// the router. It exposes one MCP tool per leaf where SurfaceMCP is
// enabled. Tool name = dotted leaf path (e.g. "widget.add").
//
// Supported MCP methods:
//
//	initialize  → server info + capabilities
//	tools/list  → enumerates tools with inputSchema derived from flags
//	tools/call  → invokes the leaf via bridge.Invoke; result rendered
//	              as MCP content blocks
//
// Behavior:
//   - Forces inv.Meta.Surface = SurfaceMCP for every call.
//   - Maps flags from request "arguments" into inv.Flags; values are
//     forwarded as-is (the bridge re-renders them with %v at apply
//     time, so the cobra leaf parses them as strings).
//   - Result.Stdout becomes a text content block. Result.Stderr (if
//     any) becomes a second text block tagged "[stderr] ...". Non-zero
//     ExitCode sets isError:true.
func MountMCP(b *Bridge, r *api.Router, opts ...MCPOption) error {
	if b == nil {
		return errors.New("cmdsurface: MountMCP: nil bridge")
	}
	if r == nil {
		return errors.New("cmdsurface: MountMCP: nil router")
	}
	cfg := mcpConfig{
		path:          defaultMCPPath,
		serverName:    defaultMCPServerName,
		serverVersion: defaultMCPServerVersion,
	}
	for _, o := range opts {
		o(&cfg)
	}
	h := &mcpHandler{b: b, cfg: cfg}
	r.Handle(http.MethodPost, cfg.path, h.serveHTTP)
	return nil
}

// mcpHandler holds the bridge + configuration for one mounted MCP
// endpoint. Stateless across requests; safe to share across goroutines
// (state lives on Bridge and Runner).
type mcpHandler struct {
	b   *Bridge
	cfg mcpConfig
}

// jsonRPCRequest is the decoded request envelope. We tolerate id
// being any JSON scalar — clients send numbers, strings, or null.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCError is the JSON-RPC error object.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// jsonRPCResponse is the response envelope. Exactly one of Result or
// Error is set on success / failure respectively.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// serveHTTP is the JSON-RPC dispatcher. It decodes one request,
// routes by method, and writes one response.
func (h *mcpHandler) serveHTTP(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		writeJSONRPCError(w, nil, mcpErrInternal, "read request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var rpc jsonRPCRequest
	if err := json.Unmarshal(body, &rpc); err != nil {
		writeJSONRPCError(w, nil, mcpErrParse, "parse error: "+err.Error(), http.StatusBadRequest)
		return
	}
	if rpc.JSONRPC != "" && rpc.JSONRPC != "2.0" {
		writeJSONRPCError(w, rpc.ID, mcpErrInvalidRequest, "invalid jsonrpc version", http.StatusBadRequest)
		return
	}

	switch rpc.Method {
	case "initialize":
		h.handleInitialize(w, rpc)
	case "tools/list":
		h.handleToolsList(w, rpc)
	case "tools/call":
		h.handleToolsCall(w, req, rpc)
	default:
		writeJSONRPCError(w, rpc.ID, mcpErrMethodNotFound, "method not found: "+rpc.Method, http.StatusOK)
	}
}

// handleInitialize returns the minimal MCP initialize response with
// protocol version, capabilities, and server identity.
func (h *mcpHandler) handleInitialize(w http.ResponseWriter, rpc jsonRPCRequest) {
	writeJSONRPCResult(w, rpc.ID, map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    h.cfg.serverName,
			"version": h.cfg.serverVersion,
		},
	}, http.StatusOK)
}

// writeJSONRPCResult writes a successful JSON-RPC envelope.
func writeJSONRPCResult(w http.ResponseWriter, id json.RawMessage, result any, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

// writeJSONRPCError writes a JSON-RPC error envelope.
func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: msg},
	})
}

// toolName renders a leaf path as a dotted MCP tool name.
//
//	[]string{"widget","add"} -> "widget.add"
func toolName(path []string) string { return strings.Join(path, ".") }

// pathFromToolName splits a dotted MCP tool name back into a leaf
// path slice. Empty input returns nil.
func pathFromToolName(name string) []string {
	if name == "" {
		return nil
	}
	return strings.Split(name, ".")
}

// mcpJSONType maps a pflag type string to the corresponding JSON
// Schema primitive. Duplicated (intentionally) from
// go/ai/toolspec/adapters/mcp.go — see top-of-file comment.
func mcpJSONType(pflagType string) string {
	switch pflagType {
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"count":
		return "integer"
	case "float32", "float64":
		return "number"
	case "stringArray", "stringSlice", "intSlice", "boolSlice":
		return "array"
	default:
		return "string"
	}
}

// flagProperty maps one pflag.Flag to a JSON Schema property object.
func flagProperty(f *pflag.Flag) map[string]any {
	t := mcpJSONType(f.Value.Type())
	prop := map[string]any{
		"type":        t,
		"description": f.Usage,
	}
	if t == "array" {
		prop["items"] = map[string]string{"type": "string"}
	}
	return prop
}

// isFlagRequired reports whether the cobra `MarkFlagRequired`
// annotation is set on f.
func isFlagRequired(f *pflag.Flag) bool {
	_, ok := f.Annotations[cobra.BashCompOneRequiredFlag]
	return ok
}

// collectFlags walks both inherited and local flags of cmd and
// returns the schema properties + required-name list, filtering out
// hidden / deprecated flags.
//
// Local flags override inherited ones of the same name; we ensure
// each flag is emitted exactly once.
func collectFlags(cmd *cobra.Command) (map[string]any, []string) {
	props := make(map[string]any)
	var required []string
	seen := make(map[string]bool)

	visit := func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" {
			return
		}
		if seen[f.Name] {
			return
		}
		seen[f.Name] = true
		props[f.Name] = flagProperty(f)
		if isFlagRequired(f) {
			required = append(required, f.Name)
		}
	}

	// Local first so locally-overridden flag annotations win.
	cmd.LocalFlags().VisitAll(visit)
	cmd.InheritedFlags().VisitAll(visit)
	return props, required
}
