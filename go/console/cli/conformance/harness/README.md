# harness — xrr-backed integration test toolkit

`hop.top/kit/go/console/cli/conformance/harness` is the Go test
helper package adopters import to assert kit-blessed contract
properties of a cobra-driven CLI.

It complements `kitconformance.AssertCLI` (the static-shape
checker shipped by 12fcc-leak): where `AssertCLI` validates the
command tree at registration time, the harness drives the CLI
under controlled conditions and inspects the externally visible
result — exit codes, JSON output, xrr cassettes of HTTP / SQL /
Redis / gRPC / exec / fs interactions, gated destructive paths.

## Quick start

```go
import (
    "testing"

    "hop.top/kit/go/console/cli/conformance/harness"
)

func TestSpaced_Idempotent(t *testing.T) {
    cmd := buildRoot().Cmd
    harness.PlanApplyReplay(t, cmd,
        harness.Args("launch", "--payload", "alpha"))
}

func TestSpaced_DryRunNoMutation(t *testing.T) {
    cmd := buildRoot().Cmd
    harness.AssertDryRunNoMutation(t, cmd,
        harness.Args("launch", "--payload", "alpha"))
}
```

Each primitive accepts a variadic `...harness.Option`. Options
compose; see [Options](#options) for the full list.

## Primitives

| Primitive | Asserts |
|-----------|---------|
| `PlanApplyReplay`            | second apply over the same args produces an empty cassette diff against the first |
| `AssertDryRunNoMutation`     | every interaction recorded with `--dry-run` classifies as `Read` |
| `AssertDestructiveGated`     | command refuses without `--confirm=yes`, proceeds with it, no-ops when paired with `--dry-run` |
| `AssertExitCodeClass`        | observed exit code falls in the leaf's `kit/exit-codes` annotation |
| `AssertJSONSchema`           | stdout JSON validates against `kit/output-schema` |
| `AssertCapabilityRoundtrip`  | every non-interactive leaf accepts `--help` |

Each primitive operates on `*cobra.Command`. Adopters with a
non-cobra invocation surface can implement `harness.Invoker` and
pass `harness.WithInvoker(...)`.

## Wiring xrr into your adapter call sites

The harness depends on **xrr** (`hop.top/xrr`) to capture side
effects. xrr does *not* auto-instrument; the adopter wraps each
adapter call site once. Example for HTTP:

```go
import (
    xrr "hop.top/xrr"
    xrrhttp "hop.top/xrr/adapters/http"
)

func fetchMission(ctx context.Context, id string) error {
    sess, err := xrr.SessionFromEnv()
    if err != nil { return err }
    defer sess.Close()

    adapter := xrrhttp.NewAdapter()
    req := &xrrhttp.Request{Method: "GET", URL: "/missions/" + id}
    _, err = sess.Record(ctx, adapter, req, func() (xrr.Response, error) {
        // real HTTP call here
        return &xrrhttp.Response{Status: 200}, nil
    })
    return err
}
```

The harness exports `XRR_MODE` and `XRR_CASSETTE_DIR` before each
invocation; `xrr.SessionFromEnv` picks them up automatically. In
tests where the adopter prefers to construct its own session,
`harness.WithCassetteDir(path)` and `harness.WithMode(mode)` are
the equivalent in-process knobs.

## Mutation classifier

`AssertDryRunNoMutation` and the modified-entry annotation in
`PlanApplyReplay` route every cassette interaction through a
per-adapter mutation classifier:

| Adapter | Default classifier |
|---------|--------------------|
| http    | RFC 7231 — `GET/HEAD/OPTIONS` → Read; `POST/PUT/PATCH` → Write; `DELETE` → Destructive |
| sql     | First verb of normalized query — `SELECT/SHOW/EXPLAIN` → Read; `INSERT/UPDATE/CREATE` → Write; `DELETE/DROP/TRUNCATE` → Destructive |
| redis   | ~120-entry static table sourced from Redis 7.x docs; subcommand-aware for `CLUSTER`, `MEMORY`, `CLIENT`, `SCRIPT`, `FUNCTION`, `DEBUG`, `CONFIG` |
| grpc    | Method-name prefix — `Get*/List*/Watch*` → Read; `Create*/Update*/Set*` → Write; `Delete*/Purge*/Reset*` → Destructive |
| fs      | Op enum — `write/mkdir/chmod` → Write; `remove/rename/truncate` → Destructive (xrr fs adapter is mutations-only by design) |
| exec    | Conservative default — every call is `Write`. Override via `harness.WithExecClassifier(fn)` |

Overrides:

```go
// gRPC methods that don't follow the verb-noun convention.
harness.WithGRPCClassifier(func(service, method string) classifier.Class {
    if method == "Heartbeat" {
        return classifier.ClassRead
    }
    return classifier.ClassWrite
})

// Exec subprocess catalog — adopters know their tools.
harness.WithExecClassifier(func(argv []string) classifier.Class {
    switch argv[0] {
    case "ls", "cat", "git":
        return classifier.ClassRead
    case "rm", "mv":
        return classifier.ClassDestructive
    }
    return classifier.ClassWrite
})
```

## Annotations the harness reads

| Annotation | Used by | Default when absent |
|------------|---------|---------------------|
| `kit/side-effect`    | every primitive (filters interactive leaves, decides destructive paths) | required for `AssertDryRunNoMutation` / `AssertDestructiveGated` |
| `kit/exit-codes`     | `AssertExitCodeClass` | expects `OK` with a hint in the failure message |
| `kit/output-schema`  | `AssertJSONSchema` | `harness.WithSchema([]byte)` override required if absent |
| `kit/output-schema-version` | `AssertJSONSchema` (version-drift check) | skipped |
| `kit/format-flag`    | `AssertJSONSchema` (how to elicit JSON output) | `--format=json` |
| `kit/destructive-token` | `AssertDestructiveGated` (skips case 2 if a typed token is required) | unset → standard flow |

Set them at registration time via the kit typed setters:

```go
import kitcli "hop.top/kit/go/console/cli"

kitcli.SetSideEffect(cmd, kitcli.SideEffectDestructive)
kitcli.SetOutputSchema(cmd, kitcli.OutputSchema{
    Type:    &MyOutput{},
    Version: "1",
})
kitcli.SetFormatFlag(cmd, "--format=json")
cmd.Annotations["kit/exit-codes"] = "OK,NOT_FOUND"
```

## Options

```go
harness.Args(args ...string)                 // argv after root
harness.WithMode(m xrr.Mode)                 // record | replay | passthrough
harness.WithCassetteDir(path string)         // persistent cassette dir
harness.WithEnv(k, v string)                 // scoped env var
harness.WithStdin(r io.Reader)               // pipe stdin
harness.NonTTY()                             // self-doc; no-op default
harness.WithTTY()                            // simulate tty (kit probe seam)
harness.WithConfigSnapshot(map[string]any)   // pin viper for one call
harness.WithConfigSnapshotFile(path string)  // same, from YAML/JSON file
harness.WithExecClassifier(fn)               // adopter exec rule
harness.WithGRPCClassifier(fn)               // adopter grpc rule
harness.WithExpectedClass(classes ...string) // override kit/exit-codes
harness.WithSchema(schemaJSON []byte)        // override kit/output-schema
harness.WithParallelism(n int)               // advisory (capability roundtrip)
harness.WithFailFast()                       // capability roundtrip stop-on-first
harness.WithLeafExitOverride(map[string]string) // per-leaf expected exit
harness.WithInvoker(inv Invoker)             // bypass cobra (escape hatch)
```

## Failure-message shapes

Every Assert* prints a structured failure with file/leaf
identification, expected vs. observed, and a suggested fix. Sample
(from `PlanApplyReplay` failure):

```
PlanApplyReplay: cassette diff non-empty (2 diffs)

  + http POST /api/v1/missions   → status 201
    (apply #2 issued an extra POST not seen in apply #1)

  + sql INSERT INTO missions (id) VALUES ($1)
    (apply #2 inserted; idempotency broken)

cassettes-1: /tmp/TestSpaced_Idempotent/apply-1
cassettes-2: /tmp/TestSpaced_Idempotent/apply-2
```

Adopters running under `go test -v` get the message verbatim;
`go test -json` consumers parse it via the standard Output events.

## Hazards & mitigations

- **Cassette drift.** A cassette recorded today binds your tests to
  the upstream API's day-1 shape. Refresh on a schedule via
  `harness.WithMode(xrr.ModeRecord)` in a tagged sub-test, or
  re-record before every release.
- **Non-deterministic bodies.** UUIDs, timestamps, request IDs
  embedded in outbound bodies destabilize fingerprints. Provide
  a pre-fingerprint scrubber on the adopter side (xrr ships
  request-rewriting hooks per adapter).
- **`WithConfigSnapshot` and parallel tests.** viper is global
  state; `WithConfigSnapshot` mutex-guards against concurrent
  use. Co-scheduled tests that both install snapshots run
  sequentially.
- **Cross-runtime cassettes.** Cassettes recorded by the Go
  harness are Go-only for v1 — `xrr` ports in ts/py/rs/php may
  diverge on fingerprint extensions.

## Related

- ADR-0021 — xrr-first integration model.
- `kitconformance.AssertCLI` — Layer-A static-shape checker.
- `hop.top/xrr` — the cassette substrate.
