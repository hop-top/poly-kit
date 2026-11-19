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
route, with one exception: `GET /v1/dek/hello` passes the
authentication guard, because a dek client calls it before it holds a
credential. Destructive commands are exposed by the same switch as
REST (`Policy.AllowDestructiveOn` with `SurfaceREST`): a command REST
withholds is not a capability.

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
| `GET /v1/dek/hello` | yes | no credential; `contract` 1.33, `core_id`, `manifest_etag` |
| `GET /v1/dek/whoami` | yes | the router's verified principal and tenant; a fixed `local` principal when the listener has no authentication |
| `GET /v1/dek/capabilities` | yes | `Capability[]`, `ETag`, 304 on `If-None-Match` |
| `POST /v1/dek/invoke` | yes | result, plan or accepted |
| `POST /v1/dek/plans/{id}/apply` | yes | applied, drifted or blocked |
| `GET /v1/dek/jobs`, `GET /v1/dek/jobs/{id}` | yes | the caller's own jobs; the list holds the active ones |
| `POST /v1/dek/auth/token` | no | dek calls it only when its host holds a user and secret; with a bearer in its profile it never does |
| `GET /blocks`, `/proposals`, `/workspaces` | no | |
| bus socket | no | see extension points |

Errors are `{"reason": "..."}` with the status that names the failure:
404 unknown capability, plan or job, 422 bad arguments or a missing
plan, 403 a refusal by policy or gate, 401 unauthenticated, 409 an
idempotency key used on another plan.

`hello` carries no `stream`, `head`, `heartbeat_ms` or `storage`: there
is no bus yet, and the contract marks them optional.

### Contract revision

The hello advertises 1.33 (`ContractMinor`): the revision whose schemas
the tests check against. A client refuses a different major and gates
features on the minor, so a core must not claim a revision it does not
serve. Refresh the schema copies and the constant together.

dek's client treats a surface as served unless its route answers 404 or
405; the routes not served here answer 404.

## Annotations to capabilities

Every invocable command becomes one capability, in id order.
Non-invocable commands (interactive, withheld, reserved, refused by
policy) are not listed; the REST discovery document keeps their
reasons.

| Capability field | From |
|------------------|------|
| `id`, `noun`, `verb` | command path, dot-joined; last segment is the verb, the rest the noun. A one-segment command takes the tool name as noun and `<tool>.<verb>` as id |
| `args_schema` | flags typed from the pflag type, positionals as strings; `additionalProperties: false`; `required` from required flags and arguments; kit's global flags (`--format`, `--quiet`, `--dry-run`, ...) are left out; a call carries `dry_run` itself |
| `read` | `kit/side-effect: read` |
| `destructive` | `kit/side-effect: destructive*` |
| `dry_run` (a plan is needed first) | destructive |
| `output` | `plan` when `dry_run`; `list` when the declared output schema is an array; else `record` |
| `gate` | `free`; `apply` when destructive; `DekConfig.Gate` overrides per command |
| long-running | `kit/long-running: "true"` (`cmdmeta.KeyLongRunning`); not a manifest field, it decides `accepted` |
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
  command honors `--dry-run`). The plan is stored for the caller. A
  command that opted out of `--dry-run` answers with its refusal.
- Destructive without `dry_run`: 422 `capability needs a plan`.
- Class `apply` or any other beyond `free`, without `dry_run`: 403.
- Long-running without `dry_run`: `{"kind":"accepted","body":{"job_id"}}`.
- Non-read calls need a non-empty `idempotency_key`.

The call goes through the same executor as REST: the permission gate,
audit sinks, confirmation and idempotency are the bridge's. A dry run
reaches the executor with no idempotency key, so an executor that
replays by key cannot answer the real run with the plan.

## Apply

`POST /v1/dek/plans/{id}/apply`, with an optional `Idempotency-Key`
header (else the key the plan was made under):

1. Only the principal that made the plan applies it; another caller gets
   404.
2. The same key replays the recorded outcome byte for byte, without
   running anything. The key on another plan is 409. A plan applies
   once: a new key on an applied plan answers `blocked`.
3. A fresh dry run is compared with the plan the operator reviewed.
   `generated_at`, the timestamp `cli.Plan` stamps on every run, is
   ignored; a captured stdout that is JSON is compared as JSON. A
   difference answers `drifted` with a new stored plan (`fresh`) and
   `diff` `{from, to}`, and runs nothing.
4. Otherwise the command runs, with `--confirm=yes` when it asks for
   confirmation: the reviewed plan is the confirmation. A command that
   needs a typed confirm token (`kit/destructive-token`) answers
   `blocked`, since an apply cannot give one.
5. Classes other than `free` and `apply` answer `blocked`: this core has
   no review. `min_approvals` in the body is refused with 422, as the
   contract requires of a core that cannot honour it.
6. A long-running command applies as a job: `applied` carries
   `{"job_id"}`.

Plans live in a `PlanStore`; the default is in memory and lost on
restart (`Config.Plans` replaces it). Applies on one plan are
serialized.

## Jobs

A long-running invoke or apply starts the command in the background on
the core's own context (it ends with the service, not with the request)
and answers at once. A job is `queued`, `running`, then `succeeded` or
`failed`, with `outcome` `{"result": ...}` or `{"error": "..."}`. At most
`MaxJobs` (default 16) run at once; past that the call answers 503.
Jobs are owned by the principal that started them: `GET /jobs` lists
that principal's active jobs, `GET /jobs/{id}` any of its jobs.
Retries, waiting and expiry are not modelled: `max_attempts` is 1.

## Extension points (not built)

- **Proposals and approval classes.** A class beyond `free` and `apply`
  should raise a proposal that people decide; needs the class model and
  a proposal store. Until then those classes are blocked, never
  waived.
- **Streaming and bus.** `CommandStreamer` already streams a command's
  output as server-sent events on the REST projection. The dek bus is a
  separate WebSocket: `hello` with `stream`/`head`/`heartbeat_ms`, then
  CloudEvents with resume offsets. The seam is a `Bus` interface on
  `Core` fed by job and plan changes; `OutputKind` `stream` is reserved
  for capabilities that stream. Without it a client polls `/jobs`.
- **Persistence.** `PlanStore` and `JobStore` are the seams.
- **Blocks, workspaces, token.** Not served; see the table.

## Conformance

The tests check every body (capabilities, hello, whoami, invoke
results and plans, apply responses, jobs) against copies of the
contract's JSON Schemas under `go/transport/dek/testdata/contract`, and
the manifest against a golden file (`go test ./go/transport/dek -update`
rewrites it). The wire types are mirrored by hand: the contract ships
schemas, not a Go generator, and the surface is small.

The service tests drive a real command tree through the bridge over a
unix socket: the connect sequence behind bearer auth, a read, a
destructive command planned and applied, and a long-running command run
as a job.

dek's own client crate (`dekc`) was run against a kit tool over TCP
loopback: connect (hello without a credential), `whoami`, typed
capabilities, a read, a destructive plan then apply and its replay, and
a long-running job. dek's client dials `http` loopback or `https`, not a
unix socket, so a real dek reaches a kit tool over TCP; the unix socket
is for local clients that dial one.
