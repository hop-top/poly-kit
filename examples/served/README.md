# served

Conformance fixture for kit's zero-wiring serve capability.

`main.go` is a kit CLI built with `cli.New` and five options — the
reserved `status` verb, the kit-shipped `api` and `socket` services,
one adopter-owned service, and a bus — plus one command per class the
[serve-lifecycle contract](../../docs/contracts/serve-lifecycle.md)
distinguishes. Nothing is mounted by hand. `served_test.go` drives the
real `Execute` path (the one that installs the confirmation and policy
gates) with the arguments an operator would type and asserts every
claim the contract makes about a conformant application command.

## Command tree

| Command      | Class              | Served as                                                  |
|--------------|--------------------|------------------------------------------------------------|
| `item list`  | read, output schema | `GET /v1/commands/item/list`, answers in `data`            |
| `item add`   | write-local        | `POST /v1/commands/item/add`                               |
| `item purge` | destructive-shared | withheld (`unauthorized-destructive`) until a surface is named, then needs `confirm` |
| `shell`      | interactive        | never: 404 + `interactive` over REST, `NOT_INVOCABLE` over the socket |
| `upgrade`    | `kit/self-hosting` | never: 404 + `self-hosting` over REST, `NOT_FOUND` over the socket |
| `serve`      | kit's own          | never: `self-hosting`                                      |
| `status`     | reserved           | never: `management-only`                                   |

## What the tests prove

Each test name is the claim it pins:

- `TestServeExistsWithContractFlagsAndChildren` — `serve` carries
  `--list`, `--enable`, `--disable`, the three timeouts, `--addr`
  defaulting to `127.0.0.1:8080`, `--insecure-remote`, `--socket`; the
  registry lists `api`, `socket`, `heartbeat` in registration order.
- `TestServeListNamesEveryService` — `serve --list` mirrors that order.
- `TestReadinessReachesTheBusAndTheLog` —
  `kit.serve.service.ready_reported` carries the bound address; the
  log counterpart carries it under `address=`; nothing prints
  `Listening on`; a signal-initiated stop is a clean stop.
- `TestDiscoveryDescribesEveryCommandWithItsReason` — every command
  above with its `invocable` and `reason`; nothing else is invocable.
- `TestReadAndWriteRunOverREST`, `TestReadAndWriteRunOverTheSocket` —
  a read answers in `data` with `stdout` empty; a write runs and the
  next read sees it.
- `TestDestructiveIsWithheldOverRESTByDefault`,
  `TestDestructiveIsRefusedOverTheSocketByDefault` — 404 with the
  discovery reason; `BLOCKED` over the socket.
- `TestDestructiveRunsOverRESTOnceNamedAndConfirmed`,
  `TestDestructiveRunsOverTheSocketOnceNamedAndConfirmed` — with
  `Policy.AllowDestructiveOn` naming the surface, the command's own
  gate refuses without `confirm` (exit 5, 403 over REST) and runs with
  it.
- `TestInteractiveAndSelfHostingAreNeverInvocableOverREST`,
  `TestInteractiveAndSelfHostingAreNeverInvocableOverTheSocket` — with
  every ceiling lifted, these classes still never run.
- `TestUnauthenticatedRemoteServingIsRefused` — `--addr 0.0.0.0:0`
  exits 2 with a message naming the three remedies.
- `TestInsecureRemoteOptInIsHonoredByName` — `--insecure-remote`
  lifts the refusal and changes nothing else.
- `TestAdopterServiceStartsUnderTheSupervisor` — `serve heartbeat`
  starts the adopter's service; `serve --enable heartbeat` starts it
  beside `api` and the supervisor reports aggregate readiness.

## Run

```sh
go run ./examples/served item list
go run ./examples/served serve --list
go run ./examples/served serve api --addr 127.0.0.1:0
```

## Test

```sh
go test -race ./examples/served/
```

The fixture is the reference a template or an adopter can diff their
own root against: if a claim holds here and not in your tool, the
difference is in your wiring.
