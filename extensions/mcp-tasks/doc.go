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
// 2c1425d9a288b9b1f489430fe1e00bb392b47e48 (dated 2026-07-15, fetched
// 2026-08-13); wire shapes follow that revision exactly.
//
// A host wires the extension around an existing *mcp.Server:
//
//	ext := tasks.New(&tasks.Options{ /* store, TTL, principal */ })
//
//	so := &mcp.ServerOptions{ /* ... */ }
//	tasks.DeclareServerCapability(so)           // capabilities.extensions
//	server := mcp.NewServer(impl, so)
//	if err := ext.Attach(server); err != nil {  // tasks/* + result shape
//	    return err
//	}
//
//	http.Handle("/mcp", mcp.NewStreamableHTTPHandler(getServer, nil))
//
// Attach registers tasks/get, tasks/update and tasks/cancel on the
// server through the SDK's own custom-method registration, so the
// SDK's HTTP handler dispatches them exactly as it does a standard
// method. The extension adds no HTTP handler of its own: every
// tasks/* request passes the same Host, cross-origin, body-size,
// content-negotiation, protocol-version and session checks as any
// other request before it can reach task state.
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
