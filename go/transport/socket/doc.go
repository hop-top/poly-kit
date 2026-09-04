// Package socket serves a tool's command tree over a Unix domain
// socket, as the `socket` transport service.
//
// # Wire protocol
//
// Newline-delimited JSON, one request object per line, one response
// object per line, in the order the requests arrived on that
// connection. A request is a [hop.top/kit/go/transport/cmdsurface.Invocation];
// a response is a [Response] wrapping either a
// [hop.top/kit/go/transport/cmdsurface.Result] or an error.
//
//	--> {"path":["widget","list"],"flags":{"format":"json"}}
//	<-- {"ok":true,"result":{"exit_code":0,"stdout":"…"}}
//
//	--> {"path":["nope"]}
//	<-- {"ok":false,"error":{"code":"NOT_FOUND","message":"…"}}
//
// NDJSON rather than the existing ConnectRPC transport in
// [hop.top/kit/go/transport/rpc]: that package is HTTP/2 plus
// protobuf and dispatches to generated per-service handlers, so
// projecting an arbitrary cobra tree onto it would mean generating a
// schema per adopter tool. The command tree is already described
// dynamically by [hop.top/kit/go/ai/cmdreflect], and the invocation
// shape is already JSON-tagged, so a line-delimited JSON framing
// carries it with no codegen and no schema to keep in sync. A caller
// that wants protobuf over a socket wires the rpc server onto a
// listener directly; this service is the zero-configuration local
// channel.
//
// # Trust model
//
// A Unix domain socket is loopback-only by construction: it has no
// port, is not routable, and is reachable only by a process that can
// open the path. Access control is therefore filesystem access
// control — the socket is created under the tool's XDG runtime
// directory with mode 0600, owner-only.
//
// The service does not authenticate callers beyond that. Remote
// access and per-principal authorization are out of scope here; the
// seam carries them when they land, because [Request] already accepts
// a caller identity and trace id, and those travel into
// [hop.top/kit/go/transport/cmdsurface.Meta] where the policy gate
// and audit sinks read them. Today a caller-supplied identity is
// provenance for the audit trail, not a credential — nothing is
// granted on its basis.
package socket
