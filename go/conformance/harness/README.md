# harness

## What it answers

Whether a cobra-driven CLI actually behaves as its annotations claim:
exit codes, JSON output, gated destructive paths, and the xrr
cassettes of the HTTP / SQL / Redis / gRPC / exec / fs interactions it
produced. It complements `kitconformance.AssertCLI`, which validates
the command tree at registration time; the harness drives the CLI
under controlled conditions and inspects the externally visible
result.

## Use it when

- assert a second apply changes nothing → `harness.PlanApplyReplay`
- assert `--dry-run` records only reads → `harness.AssertDryRunNoMutation`
- assert a destructive leaf refuses without `--confirm=yes` → `harness.AssertDestructiveGated`
- assert the exit code falls in `kit/exit-codes` → `harness.AssertExitCodeClass`
- assert stdout validates against `kit/output-schema` → `harness.AssertJSONSchema`
- assert every non-interactive leaf accepts `--help` → `harness.AssertCapabilityRoundtrip`
- teach the classifier your own verbs → `harness.WithGRPCClassifier`, `harness.WithExecClassifier`
- drive a non-cobra surface → implement `harness.Invoker`, pass `harness.WithInvoker`

## Quick start

```go
import "hop.top/kit/go/conformance/harness"

func TestSpaced_Idempotent(t *testing.T) {
    cmd := buildRoot().Cmd
    harness.PlanApplyReplay(t, cmd,
        harness.Args("launch", "--payload", "alpha"))
}
```

Each primitive accepts a variadic `...harness.Option`; options
compose.

## Contract

- The harness depends on **xrr** (`hop.top/xrr`, `v0.1.0-alpha.3`) to capture side effects. xrr does *not* auto-instrument: the adopter wraps each adapter call site once.
- `XRR_MODE` and `XRR_CASSETTE_DIR` are exported before each invocation, so `xrr.SessionFromEnv` picks them up; `harness.WithCassetteDir` and `harness.WithMode` are the in-process equivalents.
- Every primitive operates on `*cobra.Command` unless `WithInvoker` replaces the surface.
- Annotations read: `kit/side-effect` (every primitive; required for the dry-run and destructive assertions), `kit/exit-codes`, `kit/output-schema`, `kit/output-schema-version`, `kit/format-flag` (default `--format=json`), `kit/destructive-token`.
- Default classifiers: http by RFC 7231 method, sql by first verb of the normalized query, redis by a ~120-entry table sourced from Redis 7.x docs (subcommand-aware), grpc by method-name prefix, fs by op enum, exec conservatively as `Write` for every call.
- `WithConfigSnapshot` mutex-guards viper, so co-scheduled tests that both install snapshots run sequentially.
- Cassettes recorded by the Go harness are Go-only for v1; xrr ports in ts/py/rs/php may diverge on fingerprint extensions.

## Neighbours

- [`classifier/`](classifier/): per-adapter mutation classifiers onto the closed `Class` enum (`ClassRead`, `ClassWrite`, `ClassDestructive`).
- [`diff/`](diff/): cassette directory diff used by `PlanApplyReplay`; two dirs are equal iff the set of (adapter, fingerprint) pairs is the same.
- [`predicates/`](predicates/): `testing.T`-free kernel returning `(ok bool, summary string)`; the scenario grader reuses it.
- `go/conformance`: `AssertCLI`, the Layer-A static-shape checker.
- `go/conformance/recorder`: stamps `recorder_version` from the xrr module version in build info, so a bump moves recorded manifests too.

## See also

- [Conformance reference](../../../docs/adopters/reference/conformance.md#integration-harness): every primitive, the xrr wiring example, the full classifier and annotation tables, every option, failure-message shapes, hazards and mitigations
- ADR-0021: the xrr-first integration model

<!-- release: track hop.top/xrr v0.1.0-alpha.3 -->
