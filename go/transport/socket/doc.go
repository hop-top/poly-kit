// Package socket serves a tool's command tree over a Unix domain
// socket, as the `socket` transport service.
//
// The wire protocol is newline-delimited JSON: one [Request] object
// per line in, one [Response] per line out, answered in order. See
// README.md in this directory for the field-by-field schemas, the
// error-code table, and the configuration keys; and the adopter
// guide docs/adopters/guides/serve-cli-over-unix-socket.md for the
// task walkthrough.
//
// NDJSON rather than the existing ConnectRPC transport in
// [hop.top/kit/go/transport/rpc]: that package is HTTP/2 plus
// protobuf and dispatches to generated per-service handlers, so
// projecting an arbitrary cobra tree onto it would mean generating a
// schema per adopter tool. The command tree is already described
// dynamically by [hop.top/kit/go/ai/cmdreflect], and the invocation
// shape is already JSON-tagged, so a line-delimited framing carries
// it with no codegen and no schema to keep in sync.
//
// # Trust model
//
// A Unix domain socket is loopback-only by construction: it has no
// port, is not routable, and is reachable only by a process that can
// open the path. Access control is therefore filesystem access
// control, and the socket is created owner-only ([SocketMode]).
//
// The service does not authenticate callers beyond that. Remote
// access and per-principal authorization are out of scope here; the
// seam carries them when they land, because [Request] already accepts
// a caller identity and trace id that travel into
// [hop.top/kit/go/transport/cmdsurface.Meta] where audit sinks read
// them. A caller-supplied identity is provenance, not a credential —
// nothing is granted on its basis.
package socket
