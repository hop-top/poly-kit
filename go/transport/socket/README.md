# socket

## What it answers

How a kit command tree is served over a Unix domain socket as
newline-delimited JSON: one request object per line, one response
object per line, answered in order. It is the local-only transport,
where the socket file's `0600` permission is the access control. REST
over TCP is `go/transport/api`; a schema-typed RPC is
`go/transport/rpc`.

## Use it when

- serve the tree on a socket → `cli.WithSocket(cli.SocketConfig{...})`
- construct the transport by hand → `socket.New(path)`
- verify each request before it runs → `Transport.Auth` (an `Authenticator`), `cli.SocketConfig.Auth`
- observe refusals → `Transport.OnRefused`, which the built-in service routes into `Bridge.Audit`
- read a response → `Request`, `Response`, `Error` wire types
- branch on a refusal → `CodeNotFound`, `CodeNotEnabled`, `CodeNotInvocable`, `CodeBlocked`, `CodeDenied`, `CodeUnauthenticated`, `CodeInvalid`, `CodeInternal`

## Quick start

```json
{"path":["widget","get"],"args":["7"],"flags":{"format":"json"}}
```

```json
{"ok":true,"result":{"exit_code":0,"data":{"id":7,"name":"bolt"}}}
```

Register it through
[`cli.WithSocket`](../../console/cli/serve_socket.go) rather than
constructing the service by hand.

## Contract

- `path` must be non-empty. Without an `Authenticator`, `caller` and `tenant` are recorded, never verified, and grant nothing.
- `ok:true` with a non-zero `exit_code` means the command ran and failed; `ok:false` means it never ran.
- A malformed line does not close the connection; the next request is served normally.
- The resolved path is made absolute. Paths longer than **103 bytes** are refused at startup with exit `2` (the lower of the platform `sun_path` bounds, so a configuration is portable).
- The socket file is created with mode **`0600`**. A stale socket file is removed and reclaimed at start; one a live process is listening on is refused with `already in use`; a path that exists and is not a socket is refused and left untouched. `Close` unlinks the file.
- A client that hangs up mid-command cancels it: up to 16 requests are read ahead, past which a flooding client is not observed until the backlog drains.
- Path precedence, highest first: `--socket <path>`, `services.socket.path` (or `<TOOL>_SERVICES_SOCKET_PATH`), `SocketConfig.Path`, `<runtime dir>/<tool>/<tool>.sock`.

## Neighbours

- `go/transport/transportsvc`: the lifecycle seam this transport implements.
- `go/transport/cmdsurface`: the bridge whose gate produces every refusal code.
- `go/transport/rpc`: ConnectRPC and protobuf, when you need a schema rather than dynamic dispatch.
- `go/console/cli`: `cli.WithSocket`, `cli.SocketConfig`, `cli.WithRootFactory`.

## See also

- [socket wire reference](../../../docs/adopters/reference/socket-wire.md): request and response fields, error codes, authentication, cancellation, configuration keys, path limits and permissions, the Go API
- [serve-cli-over-unix-socket.md](../../../docs/adopters/guides/serve-cli-over-unix-socket.md): the task walkthrough
- [serve lifecycle contract](../../../docs/contracts/serve-lifecycle.md): the Security section states the provenance and permission rules
- [secure-remote-serving.md](../../../docs/adopters/guides/secure-remote-serving.md): the permission gate and audit trail
