# socket

## What it answers

How a handler map is served over a Unix domain socket as newline-delimited JSON: one request object per
line, one response object per line, answered in order. The local-only transport, where the socket file's
`0600` permission is the access control. Port of `go/transport/socket`; REST over TCP is not here.

## Use it when

- serve commands on a socket → `SocketService::new(path, handlers)`, registered on a `serve::ServiceRegistry` as service `socket`
- map command paths to code → `Handlers::register(&["widget", "get"], |inv, cancel| async { ... })`
- name each caller by the uid the kernel reports → `.authenticator(PeerAuthenticator::new(PeerAuthConfig { require_same_uid: true, scopes: None }))`
- verify callers some other way → implement `Authenticator`
- route authentication refusals to your audit stream → `.on_refused(|invocation, reason| ...)`
- retry a request without running it twice → `.idempotency(ttl)`, then the client sets `idempotency_key`; a repeat answers `"replayed":true`
- branch on a refusal → the `CODE_*` constants (`NOT_FOUND`, `UNAUTHENTICATED`, `RATE_LIMITED` with `retry_after_ms`, `CONFLICT`, `INVALID`, `INTERNAL`, ...)

- gate commands by the caller → `.permission(|inv, meta| ...)`, or with `cli-permissions` `.served_gate(Arc<ServedGate>)` and `Handlers::annotate` for a command's tier and scopes; a refusal is `DENIED`

## Quick start

```json
{"path":["widget","get"],"args":["7"],"flags":{"format":"json"}}
```

```json
{"ok":true,"result":{"exit_code":0,"data":{"id":7,"name":"bolt"}}}
```

```rust
let mut handlers = Handlers::new();
handlers.register(&["widget", "get"], |inv: Invocation, _cancel: CancelToken| async move {
    Ok(CommandResult::data(serde_json::json!({ "id": inv.args[0] })))
})?;
let svc = Arc::new(SocketService::new("/run/user/1000/tool/tool.sock", handlers));
registry.register(svc)?; // then Supervisor::run as in `serve`
```

Verified by `tests/socket.rs`.

## Contract

- Feature `socket` (unix only) builds on `serve` and adds tokio `net` + `io-util` and `getrandom`; no new crate. Peer credentials are tokio's `UnixStream::peer_cred` (SO_PEERCRED on Linux, LOCAL_PEERCRED/LOCAL_PEERPID on macOS, FreeBSD), so no `libc`/`nix`.
- Wire types and `CODE_*` symbols are Go's. `ok:true` with a non-zero `exit_code` means the command ran and failed; `ok:false` means it never ran. A malformed line does not close the connection. `path` must be non-empty. Unknown path: `NOT_FOUND`.
- A line over 1 MiB is answered `INVALID` after the lines read before it, and the connection closes. Paths over 103 bytes are refused at start.
- The socket is `0600` from the instant its name exists: it is bound inside a fresh `0700` directory beside the target, narrowed there, then hard-linked to its name (the link fails if the name exists, so a live server is never replaced). A stale socket no process listens on is reclaimed; a live one refuses `already in use`; a non-socket path is refused untouched. Stopping unlinks only the file this process bound. The staging path adds about 11 bytes, so a path within 11 bytes of the 103-byte limit can fail at start.
- Requests are read ahead (16) while a command runs; a client that hangs up cancels the invocation in flight (the handler's `CancelToken` fires and it gets 2 s to return). As in Go, a client that half-closes its write side after sending is a hang-up too.
- Without an authenticator, `caller` and `tenant` are recorded, never verified; the identity counts as transport-established (the owner). With one, its verdict replaces the claim and the identity is verified. The peer authenticator's principal is `uid:<n>` (no user-name resolution: the standard library has no passwd lookup); it records `peer_uid`, `peer_gid`, `peer_pid` in `Meta::extra`, on refusals too.
- Idempotency is opt-in and in memory (24 h when the ttl is zero, 10 000 records): a key is scoped to its caller (verified tenant + principal, else the transport); same key + same command replays, a key in flight refuses `CONFLICT` (`idempotency_conflict`), a key for another command refuses `CONFLICT` (`idempotency_key_reused`); only a run with no error and exit code zero is recorded. Records do not survive a restart.
- A route segment is a plain word (no empty, no whitespace: `RouteError::BadSegment`), and a request path must equal the registered segments byte for byte, else `INVALID`: the gate and the handler see the command that runs.
- Permission gate: asked after the path resolves, before idempotency and the handler; `DENIED` `cmdsurface: permission denied: <path> on socket: <reason>`, also sent to `on_refused`. Without an authenticator the caller is the owner (a claimed principal matches no rule); with one, its verified identity.
- A panicking handler answers `INTERNAL` and does not take the connection or service down.
- Handlers are an explicit map; projecting a command tree onto it waits for the kit-rs CLI root.

## Neighbours

- `hop_top_kit::serve`: the lifecycle this is a `Service` of
- `go/transport/socket`: the reference; wire reference in `docs/adopters/reference/socket-wire.md`

## See also

- [serve lifecycle contract](../../../../docs/contracts/serve-lifecycle.md)
