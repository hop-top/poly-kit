# socket

Serves a kit command tree over a Unix domain socket as
newline-delimited JSON.

Task guide:
[serve-cli-over-unix-socket.md](../../../docs/adopters/guides/serve-cli-over-unix-socket.md).
This page is the wire reference.

## Wire protocol

One JSON object per line in each direction. Requests on a connection
are answered in order. A connection may carry any number of requests.

### Request

```json
{"path":["widget","get"],"args":["7"],"flags":{"format":"json"},"caller":"daemon","trace_id":"abc123"}
```

| Field | Type | Required | Meaning |
|---|---|---|---|
| `path` | `[]string` | yes | command path from root to leaf, e.g. `["widget","get"]` |
| `args` | `[]string` | no | positional arguments after the path |
| `flags` | `object` | no | flags keyed by long name; values as the command expects them |
| `caller` | `string` | no | claimed principal, forwarded to audit sinks as provenance |
| `trace_id` | `string` | no | trace identifier propagated across surfaces |

`path` must be non-empty. `caller` is **not** a credential: it is
recorded, never verified, and grants nothing.

### Response

```json
{"ok":true,"result":{"exit_code":0,"data":{"id":7,"name":"bolt"}}}
```

```json
{"ok":true,"result":{"exit_code":0,"stdout":"widget-1\nwidget-2\n"}}
```

```json
{"ok":false,"error":{"code":"NOT_FOUND","message":"cmdsurface: unknown command: nosuch"}}
```

| Field | Type | Meaning |
|---|---|---|
| `ok` | `bool` | `true` when the command ran; `false` when the request was refused before reaching it |
| `result` | `object` | present when `ok` is `true` |
| `result.exit_code` | `int` | the command's exit status; `0` on success |
| `result.stdout` | `string` | captured standard output; omitted when empty |
| `result.stderr` | `string` | captured standard error; omitted when empty |
| `result.data` | `any` | the command's declared output, decoded; omitted when the command declares no output schema or the request named a non-json `format` |
| `error` | `object` | present when `ok` is `false` |
| `error.code` | `string` | stable symbol, table below |
| `error.message` | `string` | human-readable detail |

`ok:true` with a non-zero `exit_code` means the command ran and
failed. `ok:false` means it never ran.

Which of `data` and `stdout` a result carries follows the
[execution contract](../../../docs/contracts/serve-lifecycle.md#format-selection-and-structured-output):

| Command declares an output schema | Request `flags.format` | `stdout` | `data` |
|---|---|---|---|
| yes | absent | omitted | present |
| yes | `"json"` | the JSON text | present |
| yes | any other | that rendering | omitted |
| no | anything | as the command produced it | omitted |

A request is run with the service's context, not the connection's: a
client that hangs up mid-command does not cancel it, and the result
is discarded. Stopping the service cancels every command in flight.
Requests on one connection are answered in order; across connections
the bridge's runner serializes in-process commands, one at a time.

## Error codes

| Code | Cause |
|---|---|
| `NOT_FOUND` | `path` resolves to no reachable command, including one the reflector excluded (hidden, deprecated) |
| `NOT_ENABLED` | the command exists but is not exposed on this surface |
| `BLOCKED` | destructive command refused because the policy does not name this surface |
| `INVALID` | malformed request line, or empty `path` |
| `INTERNAL` | any other runner error |

A malformed line does not close the connection; the next request is
served normally.

## Configuration

| Key | Type | Default |
|---|---|---|
| `services.socket.enabled` | bool | `false` |
| `services.socket.path` | string | `<runtime dir>/<tool>/<tool>.sock` |
| `services.socket.ready_timeout` | duration | `30s` |
| `services.socket.stop_timeout` | duration | `30s` |

Path precedence, highest first:

1. `--socket <path>`
2. `services.socket.path` (or `<TOOL>_SERVICES_SOCKET_PATH`)
3. `SocketConfig.Path`
4. `<runtime dir>/<tool>/<tool>.sock`

The runtime dir is `$XDG_RUNTIME_DIR` when set, otherwise the
platform's location for ephemeral per-user files (on macOS
`~/Library/Application Support`), falling back to the OS temp
directory.

## Path limits and permissions

- The resolved path is made absolute.
- Paths longer than **103 bytes** are refused at startup with exit
  `2`. The limit is `sockaddr_un.sun_path`: 104 bytes on macOS and
  the BSDs, 108 on Linux; the lower bound is enforced everywhere so a
  configuration is portable.
- The socket file is created with mode **`0600`**. On a Unix socket
  the filesystem permission is the access control.
- A stale socket file — one no process is listening on — is removed
  and reclaimed at start. A socket a live process is listening on is
  refused with `already in use`.
- A path that exists and is not a socket is refused, and the file is
  left untouched.
- `Close` unlinks the socket file.

## API

| Symbol | Purpose |
|---|---|
| `New(path)` | construct the transport |
| `Transport` | implements `transportsvc.Transport` |
| `Request`, `Response`, `Error` | wire types |
| `CodeNotFound`, `CodeNotEnabled`, `CodeBlocked`, `CodeInvalid`, `CodeInternal` | error-code constants |
| `SocketMode` | the `0600` the socket file is created with |

Register it through
[`cli.WithSocket`](../../console/cli/serve_socket.go) rather than
constructing the service by hand.

## See also

- [transportsvc](../transportsvc/README.md) — the lifecycle seam
- [serve lifecycle contract](../../../docs/contracts/serve-lifecycle.md)
- [`go/transport/rpc`](../rpc/) — ConnectRPC and protobuf, when you
  need a schema rather than dynamic dispatch
