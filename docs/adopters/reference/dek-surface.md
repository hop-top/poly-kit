# dek surface reference

Package reference for
[`go/transport/dek`](../../../go/transport/dek/doc.go): the core side
of the dek contract, generated from a tool's command tree. It sits
beside REST, MCP and gRPC: same reflected commands, same executor,
same gates. A kit CLI serves its own `/v1/dek`; there is no merged
gateway, and a registry (not kit) says where each tool's surface is.

## Turn it on

```go
service.WithAPI(service.APIConfig{
    Addr: "unix:/run/user/1000/mytool/api.sock", // or host:port
    Dek:  &service.DekConfig{CoreID: "mytool"},
})
```

`mytool serve api` then answers under `/v1/dek`. The surface is
mounted after the REST projection and the adopter's own routes, so an
adopter route at a `/v1/dek` path wins. Auth, host and origin checks,
body limit and every other HTTP-plane block wrap it as they wrap every
route.

## Listen address

`APIConfig.Addr`, `services.api.addr` and `--addr` take either
`host:port` or `unix:/abs/path.sock` (`unix:///abs/path.sock` also).
The general rules, for any service that adopts
[`api.Listen`](../../../go/transport/api/listen.go):

- socket file mode `0600`; removed on stop; parent directory created
  `0700` when missing;
- a stale socket file (nothing answers on it) is replaced; a live one
  is an error; a path that is not a socket is an error and is left
  alone;
- a unix address counts as loopback: no `Auth` and no
  `insecure_remote` needed. The file mode is the access control;
- the Host check answers to `localhost`, `127.0.0.1` and `::1`, so a
  client sends `Host: localhost`;
- the path must be absolute and short enough for the OS (about 100
  bytes).

The api service uses it today. The mcp and rpc services still bind
TCP only.

## Routes

| Route | Served | Notes |
|-------|--------|-------|
| `GET /v1/dek/hello` | yes | `contract` 1.34, `core_id`, `manifest_etag` |
| `GET /v1/dek/capabilities` | yes | `Capability[]`, `ETag`, 304 on `If-None-Match` |
| `POST /v1/dek/invoke` | yes | read, write and dry-run calls |
| `POST /v1/dek/plans/{id}/apply` | not yet | see extension points |
| `POST /v1/dek/auth/token`, `GET /whoami` | not yet | kit authn mints its own tokens |
| `GET /blocks`, `/proposals`, `/jobs`, `/workspaces` | not yet | |
| bus socket | not yet | see extension points |

Errors are `{"reason": "..."}` with the status that names the failure:
404 unknown capability, 422 bad arguments or a missing plan, 403 a
refusal by policy or gate, 401 unauthenticated.

`hello` carries no `stream`, `head`, `heartbeat_ms` or `storage`: there
is no bus yet, and the contract marks them optional.

## Annotations to capabilities

Every invocable command becomes one capability, in id order.
Non-invocable commands (interactive, withheld, reserved, refused by
policy) are not listed; the REST discovery document keeps their
reasons.

| Capability field | From |
|------------------|------|
| `id`, `noun`, `verb` | command path, dot-joined; last segment is the verb, the rest the noun. A one-segment command takes the tool name as noun and `<tool>.<verb>` as id |
| `args_schema` | flags typed from the pflag type, positionals as strings; `additionalProperties: false`; `required` from required flags and arguments; the kit-wide `--dry-run` flag is left out (a call carries `dry_run`) |
| `read` | `kit/side-effect: read` |
| `destructive` | `kit/side-effect: destructive*` |
| `dry_run` (a plan is needed first) | destructive |
| `output` | `plan` when `dry_run`; `list` when the declared output schema is an array; else `record` |
| `gate` | `free`; `review` when destructive; `DekConfig.Gate` overrides per command |
| `scopes` | empty |
| `targets` | all five: a kit command runs wherever the palette does |
| `granted` | true |

The tier is the resolved one (declared, else kit's destructive-name
inference), the same the REST projection uses. A flag and a positional
sharing a name fail construction: both would be one key in `args`.

## Invoke

`{capability, args, dry_run, idempotency_key}` in, `{kind, body}` out.
`args` is one flat object: flag values by name, positionals by their
declared name (strings, mapped in declared order).

- Read: runs, `{"kind":"result","body":...}`. The body is the command's
  structured data, else `{"stdout": "..."}`.
- Write: runs directly, or with `dry_run: true`.
- Dry run (write or destructive only): the command runs with
  `--dry-run`, and the answer is `{"kind":"plan","body":Plan}` with
  `rendered` holding what the command printed (a `cli.Plan` when the
  command honors `--dry-run`). A command that opted out of `--dry-run`
  answers with its refusal.
- Destructive without `dry_run`: 422 `capability needs a plan`.
- Any class other than `free` without `dry_run`: 403. Kit does not
  enforce an app's class, so it refuses to run one directly.
- Non-read calls need a non-empty `idempotency_key`; it travels to the
  executor as the idempotency key, so the bridge's replay applies.

The call goes through the same executor as REST: the permission gate,
audit sinks, confirmation and idempotency are the bridge's.

## Extension points (not built)

- **Apply.** A plan has an `id` but kit stores nothing, so
  `plans/{id}/apply` is absent and a destructive capability cannot run
  yet. Needs a plan store (the plan, its principal and idempotency key)
  and the drift check: re-run the dry run, compare, answer `applied`,
  `drifted` or `blocked`.
- **Proposals.** Destructive and gated capabilities raise a proposal
  instead of running; needs the approval-class model and a proposal
  store.
- **Jobs.** Long-running commands answer `{"kind":"accepted",
  "body":{"job_id"}}`; the job package already holds durable jobs.
  Needs a way to mark a command long-running.
- **Streaming and bus.** `CommandStreamer` already streams a command's
  output as server-sent events on the REST projection. The dek bus is a
  separate WebSocket: `hello` with `stream`/`head`/`heartbeat_ms`, then
  CloudEvents with resume offsets. The seam is a `Bus` interface on
  `Core` fed by job and proposal changes; `OutputKind` `stream` is
  reserved for capabilities that stream.
- **Principal and token.** `whoami` and `auth/token` map onto
  `authn` claims; until then a bearer mode on the api service is the
  only credential, and it gates `hello` as well.

## Conformance

The tests check every body (capabilities, hello, invoke results and
plans) against copies of the contract's JSON Schemas under
`go/transport/dek/testdata/contract`, and the manifest against a golden
file (`go test ./go/transport/dek -update` rewrites it). The wire types
are mirrored by hand: the contract ships schemas, not a Go generator,
and the surface is small. Refresh the copies when the contract version
in `ContractMinor` moves.
