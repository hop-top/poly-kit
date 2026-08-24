// Package mcpsdk projects a [hop.top/kit/go/transport/cmdsurface]
// Bridge onto the Model Context Protocol using the official MCP Go
// SDK (github.com/modelcontextprotocol/go-sdk) for the entire
// protocol layer.
//
// It is the SDK-backed alternative to the hand-rolled MCP surface
// mounted by [hop.top/kit/go/transport/cmdsurface.MountMCP]. Both
// surfaces expose the same tool set (one MCP tool per bridge leaf,
// dotted-path names, JSON Schema derived from cobra flags) and honor
// the same safety posture (per-leaf enablement, destructive policy
// ceiling, auth + confirmation gates). Adopters choose per mount:
//
//   - cmdsurface.MountMCP — zero extra dependencies, single-POST
//     JSON-RPC endpoint, protocol version 2024-11-05 only.
//   - mcpsdk.Mount — full protocol coverage (2024-11-05 through
//     2026-07-28), streamable HTTP transport with session
//     management, stateless mode, stdio serving, and every
//     protocol behavior (negotiation, validation, error shapes)
//     implemented by the official SDK.
//
// Beyond tools, the whole SDK server surface is passed through to
// adopters: prompts, resources and templates, subscriptions and
// notifications, pagination, and completions via WithServerOptions /
// WithServerConfigurator; per-line progress streaming for calls
// carrying a progress token; and a live tool list — Surface.Hide /
// Expose / Sync translate runtime enablement changes into SDK tool
// add/remove, firing tools/list_changed on connected sessions.
//
// WithTasks additionally enables the EXPERIMENTAL
// io.modelcontextprotocol/tasks extension (SEP-2663) for named
// leaves: durable pollable tool calls with tasks/get, tasks/update
// and tasks/cancel, executed detached on the bridge Runner with
// kit's safety gates enforced at task creation. The extension's wire
// behavior lives in the standalone mcpext.example/tasks module
// (in-repo under extensions/mcp-tasks); it exists beside the SDK
// only because go-sdk v1.7.0 ships no tasks support — see the
// README's tasks section for the contract and the reconcile canary.
//
// This package contains no MCP protocol logic of its own. Kit-side
// code is limited to binding bridge leaves to SDK tool handlers,
// safety gating, principal derivation, and mounting. See README.md
// in this directory for the full comparison and the honest
// tradeoffs.
package mcpsdk
