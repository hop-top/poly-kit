// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

// Package tasks implements the server side of the MCP tasks
// extension, io.modelcontextprotocol/tasks (SEP-2663), for servers
// built on the official MCP Go SDK
// (github.com/modelcontextprotocol/go-sdk).
//
// The extension is EXPERIMENTAL, like the specification it
// implements. This package is pinned to the ext-tasks draft schema at
// github.com/modelcontextprotocol/ext-tasks revision
// 2c1425d9a288b9b1f489430fe1e00bb392b47e48 (2026-08-13); wire shapes
// follow that revision exactly.
//
// A host wires the extension around an existing *mcp.Server:
//
//	ext := tasks.New(&tasks.Options{ /* store, TTL, principal */ })
//
//	so := &mcp.ServerOptions{ /* ... */ }
//	tasks.DeclareServerCapability(so)           // capabilities.extensions
//	server := mcp.NewServer(impl, so)
//	ext.Attach(server)                          // result-shape middleware
//
//	sdk := mcp.NewStreamableHTTPHandler(getServer, nil)
//	http.Handle("/mcp", ext.Handler(sdk))       // tasks/get|update|cancel
//
// Task creation is server-directed, on tools/call only. Inside a tool
// handler, the host decides per call and per client:
//
//	func myTool(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
//	    if tasks.ClientDeclares(req) {
//	        return ext.StartTask(ctx, req, runSlowOperation)
//	    }
//	    return runInline(ctx, req) // non-declaring clients get the result inline
//	}
//
// The package enforces the spec's security posture: task IDs are
// unguessable (128 bits from crypto/rand), every tasks/* request is
// authorized against the principal that created the task, and an
// unknown, expired, or foreign task ID answers one identical -32602 —
// there is no oracle for the existence of another caller's tasks, and
// no tasks/list. Push notifications (notifications/tasks over
// subscriptions/listen) are not implemented: go-sdk v1.7.0 routes
// only its own notification types onto listen streams, so a
// conformant push would require reimplementing the transport layer.
// Poll-based operation — the required core of the extension — is
// complete.
package tasks
