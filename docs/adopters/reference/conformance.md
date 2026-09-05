# Conformance reference

> `hop.top/kit/go/conformance` and its sub-packages: the Layer-A
> static checker (`AssertCLI`), the grading-service client, the
> xrr-backed integration harness, the scenario DSL and grader, and
> the story DSL. Each section is the long-form companion of the
> package README of the same name.

## Who this is for

Adopters wiring kit's conformance suite into a test file or a CI
matrix. Contributors changing the checker itself start from the
package sources.

Sections:

- [Layer-A static checker](#layer-a-static-checker): `AssertCLI`, enforcement tiers, factor map, failure modes
- [Grading client](#grading-client): `go/conformance/client`
- [Integration harness](#integration-harness): `go/conformance/harness`
- [Scenario DSL and grader](#scenario-dsl-and-grader): `go/conformance/scenario`
- [Story DSL](#story-dsl): `go/conformance/story`

Migration steps for an existing tool (annotating leaves, mounting
`status`, flipping the configurable tiers) are in
[`guides/enforce-cli-conformance.md`](../guides/enforce-cli-conformance.md).

## Layer-A static checker

`hop.top/kit/go/conformance` is the adopter-facing surface for the
**12-factor CLI conformance contract**, Wave 1 (static track). It is
the library you import from a unit test, not to be confused with
`hop.top/kit/go/console/cli/conformance`, which is the cobra command
tree behind the `kit conformance` subcommand and exports no test
helpers. Every adopter-facing library sits under this tree: the
`harness` integration toolkit and the `verifynoleak` scanners are
siblings of this package. Import the library as `kitconformance`:

```go
import kitconformance "hop.top/kit/go/conformance"

func TestCLIConforms(t *testing.T) {
    kitconformance.AssertCLI(t, buildRoot())
}
```

`AssertCLI` forces `Config.EnforceValidate=true` for its scope and
walks the entire cobra tree under `root.Cmd`. The adopter's runtime
config is untouched: the helper restores the previous value when
the assertion returns.

### What is enforced at 0.1.0-alpha.0

`EnforceValidate=true` is the **default** at this release. Adopters
who need a temporary escape hatch (negative tests, fuzz harnesses,
embedded use-cases) set `cli.Config{DisableValidate: true}`. The
flip is final: there is no migration window.

The validator runs in two passes:

#### Pass 1: shipped checks (always active when `Validate` runs)

| # | Rule | Annotation | Failure bucket |
|---|------|-----------|----------------|
| S1 | Every runnable leaf has `kit/side-effect` | `cli.SetSideEffect` | `Missing` / `Invalid` |
| S2 | Every runnable leaf has `kit/idempotent` (after auto-apply) | `cli.SetIdempotency` | `MissingIdempotency` / `InvalidIdempotency` |

#### Pass 2: Layer-A hard tier (rides `EnforceValidate=true`)

| # | Rule | Annotation / source | Failure bucket |
|---|------|---------------------|----------------|
| H1 | Runnable leaves + group nodes carry non-empty `Short` | `cmd.Short` | `MissingShort` |
| H2 | Runnable leaves carry non-empty `Long` | `cmd.Long` | `MissingLong` |
| H3 | `kit/side-effect` valid (shipped, sub-tier of H) | `cli.SetSideEffect` | `Invalid` |
| H4 | `kit/idempotent` valid (shipped, sub-tier of H) | `cli.SetIdempotency` | `InvalidIdempotency` |
| H5 | `kit/output-schema`, when declared, parses as JSON | `cli.SetOutputSchema` | `InvalidOutputSchema` |
| H6 | Root mounts a reserved `status` subcommand | `cli.WithStatus(...)` or hand-rolled `Use: "status"` | `MissingStatusSubcommand` |
| H7 | Depth-1 runnable leaves carry `kit/top-level-verb` | `cli.SetTopLevelVerb` | `UnannotatedTopLevelLeaf` |
| H8 | Top-level verb count ≤ `Config.MaxTopLevelVerbs` (default 10) | n/a — adjust `MaxTopLevelVerbs` | `TooManyTopLevelVerbs` |
| H9 | Leaves at depth ≥ 3 have `kit/hierarchical` on every intermediate, unless the depth-1 ancestor is reserved | `cli.SetHierarchical` | `UnannotatedDepthExceedance` |
| H10 | Tree depth ≤ `Config.MaxHierarchyDepth` (default 3, capped 5) | n/a — restructure tree | `HierarchyDepthExceeded` |

#### Pass 2: Layer-A configurable tier (off by default at α)

Sub-flags on `Config` flip the configurable arms piecewise. They
stay **off** at `0.1.0-alpha.0` so adopters can flip each one when
their annotation tail is ready.

| # | Rule | Config flag | Annotation / setter | Failure bucket |
|---|------|-------------|---------------------|----------------|
| C1 | Opted-out `--dry-run` on write/destructive leaves carries a 1-200-char rationale | `EnforceDryRunRationale` | `cli.SetDryRunRationale` | `MissingDryRunRationale` |
| C2 | Destructive leaves require typed-token confirmation | `EnforceDestructiveToken` | `cli.SetDestructiveToken` | `MissingDestructiveToken` |
| C3 | Runnable leaves declare `kit/examples` | `EnforceGuidance` | `cli.SetExamples` | `MissingExamples` |
| C4 | Non-read leaves declare `kit/next-steps` | `EnforceGuidance` | `cli.SetNextSteps` | `MissingNextSteps` |

`AssertCLIWithOptions` lets a CI matrix flip each flag independently:

```go
kitconformance.AssertCLIWithOptions(t, root, kitconformance.Options{
    EnforceGuidance:        true,  // gate Examples + NextSteps
    EnforceDryRunRationale: false, // not ready yet
})
```

#### Pass 2: soft warnings (informational, no fail)

| # | Rule | When |
|---|------|------|
| W1 | `kit/passthrough` annotation surfaces | `PassthroughStrictness="warn"` (default); flip to `"reject"` to promote to fail |

### What is NOT enforced

The static track is **structural**: it locks the shape of the cobra
tree and the annotation surface. The dynamic / behavioral arms ship
in companion packages:

- **Provenance check** (factors 3, 4): captured in
  `go/runtime/provenance/`. Not part of `kit.Validate`.
- **Test cassettes** (factor 9): xrr-based integration testing in
  `go/conformance/harness/` (see [Integration harness](#integration-harness)).
  Not part of `kit.Validate`.
- **AI-judged quality** (factors 4, 9 polish tier): deferred until
  structural enforcement is adopted.
- **Structured-output detection at runtime** (Pass 2 H5 extension):
  the validator cannot tell whether `RunE` calls `output.Dispatch`;
  declare `cli.SetOutputSchema` explicitly when your leaf emits
  structured data. A future `kit doctor` track will surface leaves
  that emit structured data without a declared schema.

### Factor → annotation map

| 12-factor # | Factor | Wave-1 surface | How to satisfy |
|-------------|--------|----------------|----------------|
| 1 | Discovery | `<tool> spec --format json` | `cli.RegisterSpecCommand(root, "1.1")` (the spec subcommand self-annotates) |
| 2 | Versioning | `--api-version` filter + `kit/since`, `kit/min-api-version` | `cli.SetSinceVersion`, `cli.SetMinAPIVersion` |
| 3 | Provenance | `go/runtime/provenance/` (out of scope here) | see `go/runtime/provenance/` |
| 4 | Schema | `kit/output-schema` + `kit/output-schema-version` | `cli.SetOutputSchema(cmd, cli.OutputSchema{Type: &T{}, Version: "1.0"})` |
| 5 | Side effect | `kit/side-effect` | `cli.SetSideEffect(cmd, cli.SideEffectRead)` |
| 6 | Idempotency | `kit/idempotent` | `cli.SetIdempotency(cmd, cli.IdempotencyYes)` |
| 7 | Retry | `kit/retryable` | `cli.SetRetryable(cmd, true)` |
| 8 | State | reserved `<tool> status` | `cli.WithStatus(cli.StatusConfig{})` |
| 9 | Testability | xrr cassettes (out of scope) | see `go/conformance/harness/` |
| 10 | Errors | `output.Error` envelope (already shipped via `cli.WrapRunE`) | nothing; ride the middleware |
| 11 | Observability | slog hooks + `--show-sensitive` audit | nothing; ride kit defaults |
| 12 | Evolution | `kit/deprecated-since`, `kit/removal-target`, `kit/replaced-by` | `cli.SetDeprecation` |
| — | Shape | `kit/top-level-verb`, `kit/hierarchical`, `kit/passthrough` | `cli.SetTopLevelVerb`, `cli.SetHierarchical`, `cli.SetPassthrough` |
| — | Guidance | `kit/examples`, `kit/next-steps` (configurable C3/C4) | `cli.SetExamples`, `cli.SetNextSteps` |
| — | Confirmation | `kit/destructive-token`, `kit/dry-run-rationale` (configurable C1/C2) | `cli.SetDestructiveToken`, `cli.SetDryRunRationale` |

### ValidationFailureMode

When validation fails at `Execute()`, `Config.ValidationFailureMode`
picks the response:

| Value | Behavior |
|-------|----------|
| `""` (default) `ValidationFailureExit` | Write error to stderr, `os.Exit(2)` (`ExitUsage`). Preserves the pre-flip shipped UX. |
| `"error"` `ValidationFailureError` | Return `*cli.ValidationError` from `Execute`. Pair with `cli.NewE` to also catch construction-time failures. |
| `"panic"` `ValidationFailurePanic` | Panic with the error value. Useful for debugging registration-order issues — the stack trace pinpoints the offending caller. |
| `"silent"` `ValidationFailureSilent` | Log to stderr and continue. Recovery-mode escape hatch; **discouraged** outside tooling that must boot even with a misconfigured tree. |

`cli.NewE` is the constructor-time companion: it runs `Validate` at
construction time and returns `(*Root, *ValidationError)` so adopters
who embed kit inside a larger CLI (plugin host, multi-tool harness,
server pre-boot validator) can route the failure into their own
error envelope:

```go
root, ve := cli.NewE(cfg, opts...)
if ve != nil {
    return ourEnvelope.Wrap(ve)
}
return root.Execute(ctx)
```

### Validation error envelope

Validation failures surface through `output.Error` via
`ValidationError.AsCLIError()`:

```go
err := root.Validate()
if ve, ok := err.(*cli.ValidationError); ok {
    cliErr := ve.AsCLIError()
    // cliErr.Code     == output.CodeUsage
    // cliErr.ExitCode == 2
    // cliErr.Message  == ve.Error()
}
```

The kit `WrapRunE` middleware routes adopter `RunE` errors through
the same envelope, so a misconfigured tree under `ValidationFailureError`
mode emits the same shape as a runtime usage error.

ADR-0024 (12fcc conformance contract) is forthcoming.

## Grading client

`hop.top/kit/go/conformance/client` is the Go library for the
hop.top/kit conformance grading service ("svc"). Pairs with the
`kit conformance grade` CLI leaf (see
`go/console/cli/conformance/grade/`).

```go
import (
    "context"
    "os"
    "testing"

    "hop.top/kit/go/conformance/client"
)

func TestConformsToScenario(t *testing.T) {
    c, err := client.New(
        os.Getenv("KIT_CONFORMANCE_SERVICE"),
        client.WithToken(os.Getenv("KIT_CONFORMANCE_TOKEN")),
    )
    if err != nil {
        t.Fatal(err)
    }
    res, err := c.Grade(context.Background(), client.GradeRequest{
        CassetteDir: "./testdata/cassettes/conformance",
    })
    if err != nil {
        t.Fatal(err)
    }
    if res.Verdict != client.VerdictPass {
        t.Fatalf("verdict = %q (reason: %s)", res.Verdict, res.Reason)
    }
}
```

### Constructor

```go
func New(baseURL string, opts ...Option) (*Client, error)
```

baseURL is required; an empty value returns `ErrServiceUsage` — there
is no default kit-team-hosted endpoint.

### Functional options

| Option | Default | Purpose |
|--------|---------|---------|
| `WithToken(t)` | "" | bearer token for the Authorization header |
| `WithHTTPClient(h)` | `http.Client{}` | inject a pre-configured client |
| `WithUserAgent(ua)` | `kit-conformance-client/<ver>` | override User-Agent |
| `WithMaxAttempts(n)` | 3 | retry budget (1 = no retries) |
| `WithBackoff(init, max, mult, jitter)` | 500ms, 10s, 2.0, 0.3 | retry backoff |
| `WithMaxCassetteSize(n)` | 50 MiB | packed cassette body cap |

### Methods

```go
func (c *Client) Grade(ctx context.Context, req GradeRequest) (*Result, error)
func (c *Client) Status(ctx context.Context, gradeID string) (*Result, error)
```

`Grade` packs `req.CassetteDir` deterministically, posts to
`<baseURL>/v1/grade`, and returns the typed Result. If svc responds
with 202 + a poll URL, Grade polls internally until 200 or ctx
expires. `Status` fetches a result by grade-id.

### Result type

`Result` is a type alias to `hop.top/kit/go/conformance/scenario.Result`
— the exact struct svc serializes under the `"result"` key — so the
typed decode preserves everything the grader records:

```go
type Result = scenario.Result // fields (JSON tags in parentheses):
//  ScenarioID    (scenario_id)     canonical scenario name
//  SchemaVersion (schema_version)
//  Verdict       (verdict)         pass | fail | ungradable
//  Reason        (reason)          human-readable verdict summary
//  ScoredAt      (scored_at)       time.Time, RFC3339 on the wire
//  GraderVersion (grader_version)
//  RulesVersion  (rules_version)
//  Tier          (tier)            actually-graded tier
//  Facets        (facets)          tier-2/3 per-factor rollups
//  Assertions    (assertions)      tier-3 per-assertion traces
//  JudgeTraces   (judge_traces)    tier-3 AI-judge invocations
```

Each `Assertion` (alias of `scenario.AssertionResult`) carries
`ID`, `Kind`, `Factor`, `Status`, `Observed`, `Expected`, and
`Message` — the diagnosability trail explaining why a facet failed.

### Client error envelope

The package exports typed sentinels matching the kit conformance
sentinel pattern:

Exit codes follow the 12fc taxonomy: shared classes on 0-6, grade
verdicts in kit's documented >6 band (after RATE_LIMITED=64,
PROVENANCE_MISSING=65, LEAK_DETECTED=66, CONFIG=67):

| Sentinel | Exit | Meaning |
|----------|------|---------|
| `ErrServiceUnavailable` | 6 | retry-budget exhausted on 5xx / network (transient) |
| `ErrServiceAuthFailed` | 5 | 401/403 from svc |
| `ErrServiceUsage` | 2 | 4xx other than 401/403/429 |
| `ErrCassettePack` | 1 | local pack failure |
| `ErrCassetteTooLarge` | 2 | body > `WithMaxCassetteSize` |
| `ErrManifestParse` | 2 | manifest.yaml could not be read |
| `ErrGradeFail` | 68 | verdict=fail |
| `ErrGradeUngradable` | 69 | verdict=ungradable |
| `ErrRateLimited` | 64 | 429 with no headroom (transient) |

Errors implement `errors.Is` for sentinel identity and
`AsCLIError() *output.Error` so kit's CLI middleware picks up the
exit code automatically.

`IsRetryable(err)` reports whether an error should be re-attempted
by the retry loop. Service-unavailable + rate-limited + transient
network errors are retryable; auth/usage/grade-verdict errors are
terminal.

### Cassette wire format

Packed via `Pack(dir, manifest, maxBytes)`. Deterministic gzipped tar
with manifest.yaml at the root; same dir → same bytes → same
SHA-256 → same `Idempotency-Key`.

MIME type: `application/vnd.kit.cassette+tar+gzip`.

## Integration harness

`hop.top/kit/go/conformance/harness` is the Go test
helper package adopters import to assert kit-blessed contract
properties of a cobra-driven CLI.

It complements `kitconformance.AssertCLI` (the static-shape
checker): where `AssertCLI` validates the
command tree at registration time, the harness drives the CLI
under controlled conditions and inspects the externally visible
result — exit codes, JSON output, xrr cassettes of HTTP / SQL /
Redis / gRPC / exec / fs interactions, gated destructive paths.

```go
import (
    "testing"

    "hop.top/kit/go/conformance/harness"
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

### Primitives

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

### Wiring xrr into your adapter call sites

The harness depends on **xrr** (`hop.top/xrr`, `v0.1.0-alpha.3`) to
capture side effects. xrr does *not* auto-instrument; the adopter
wraps each adapter call site once. Example for HTTP:

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

### Mutation classifier

`AssertDryRunNoMutation` and the modified-entry annotation in
`PlanApplyReplay` route every cassette interaction through a
per-adapter mutation classifier (`go/conformance/harness/classifier`):

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

### Annotations the harness reads

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

### Options

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

### Failure-message shapes

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

### Hazards and mitigations

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

### Harness sub-packages

| Path | Role |
|------|------|
| `harness/classifier` | per-adapter mutation classifiers onto the closed `Class` enum (`ClassRead`, `ClassWrite`, `ClassDestructive`) |
| `harness/diff` | cassette directory diff used by `PlanApplyReplay`; two dirs are equal iff the set of (adapter, fingerprint) pairs is the same |
| `harness/predicates` | `testing.T`-free kernel returning `(ok bool, summary string)`; the scenario grader reuses it |

Related: ADR-0021 (xrr-first integration model);
`go/conformance/recorder` stamps `recorder_version` from the xrr
module version in build info, so a bump moves recorded manifests too.

## Scenario DSL and grader

Library-side parser, validator, and grader for kit conformance
scenarios. The Go package lives at `hop.top/kit/go/conformance/scenario`;
the wire-format vocabulary (verb roster, top-level keys, compound
detection rules) lives in
[`contracts/scenario-rules.json`](../../../contracts/scenario-rules.json)
and is shared with the verify-no-leak detector.

### Package shape

```
go/conformance/scenario/
├── doc.go         package overview
├── types.go       Scenario, Step, Assertion, StoryRef, JudgeBlock
├── parser.go      ParseFile / ParseBytes (yaml.v3 KnownFields)
├── validator.go   Validate(*Scenario) → *ValidationErrors
├── grader.go      Grade(ctx, Input) → *Result
├── result.go      Result, Verdict, Status; Result.ToTier(n)
├── input.go       Input, Capture, Env
├── errors.go      AsCLIError shims for kit/output integration
├── version.go     SchemaVersion, GraderVersion, SupportedSchemaVersions
├── verbs/         closed-enum verb registry + per-verb evaluators
├── judge/         AIJudge interface + Canned stub
└── testdata/      fixture scenarios (allowlisted via leak default)
```

### Authoring a scenario

A scenario is a YAML file declaring:

1. an identifying `scenario_id`,
2. the `binary` it grades,
3. one or more `steps` (CLI invocations to capture), and
4. one or more `assertions` keyed by verb (`kind`).

Minimal example:

```yaml
schema_version: "1"
scenario_id: launch-happy-path
binary: spaced
factor_coverage: [1, 2]
tier: 3
story_ref:
  story_id: launch-mission
  story_path: stories/launch.yaml
  content_hash: "sha256:<64-hex>"
steps:
  - id: launch
    invoke: ["launch", "--payload", "alpha"]
assertions:
  - id: exits-ok
    kind: exit_code_equals
    on: launch
    factor: 1
    value: 0
  - id: stream-ok
    kind: stream_discipline_pass
    on: launch
    factor: 2
```

#### Required top-level keys

| Key | Type | Notes |
|-----|------|-------|
| `schema_version` | string | currently `"1"` |
| `scenario_id` | string | kebab-case, `^[a-z][a-z0-9._-]*$` |
| `binary` | string | the CLI being graded |
| `factor_coverage` | `[]int` | 1..12, non-empty, unique |
| `tier` | int | 1, 2, or 3 |
| `story_ref` | object | `{story_id, story_path, content_hash}` |
| `steps` | `[]Step` | non-empty |
| `assertions` | `[]Assertion` | non-empty |

#### Optional top-level keys

`description`, `engine_min_grader_version`, `judge`,
`preconditions`, `actors`, `grading`, `metadata`.

`preconditions` and `actors` are reserved for future schema versions
and ignored by the v1 grader.

### Verb roster (22 verbs, v1)

Closed enum. Adding a verb requires a `rules_version` bump in
`contracts/scenario-rules.json` plus an evaluator registration in
`verbs/`. The leak-rule consistency test
(`verbs/registry_test.go`) enforces parity between the JSON and
the Go registry.

#### Exit-code verbs

- `exit_code_equals` — `{value: int}`
- `exit_code_in` — `{values: []int}`
- `exit_code_class` — `{classes: []string}` (kit class names)

#### Output verbs

- `output_field_equals` — `{path: string, value: any, parse?: "json"|"yaml"}`
- `output_field_present` — `{path: string, parse?: "json"|"yaml"}`
- `output_field_count` — `{path: string, equals: int}`
- `output_schema_matches` — `{schema_ref?: string, schema?: object}`

Path syntax is JSONPath subset (gjson dotted notation; leading
`$.` accepted).

#### Cassette verbs

- `cassette_must_contain` — `{op_class, adapter?, match?}`
- `cassette_must_not_contain` — same shape
- `cassette_diff_equals` — `{against: step-id, expect: "empty"}`
- `cassette_diff_empty` — paired apply/replay (use `_diff_equals` instead)

`op_class` ∈ `{any, mutating, reading, destructive}`.
`adapter` ∈ `{http, sql, redis, grpc, exec, fs}`.
`match` is a closed-key predicate: `query_substring`,
`url_substring`, `method`, `command`, `argv_substring`.

#### Behavioural verbs

- `destructive_gate_required` — `{when?: {flag_absent: "--yes"}}`
- `dry_run_no_mutation` — pure check; cassette must be Read-only
- `idempotency_replay_clean` — paired apply/replay; second capture
  must be at `<on>__replay`
- `capability_roundtrip` — `{leaves?: []string}`

#### Stream / stderr verbs

- `stderr_contains` — `{value: string, regex?: bool}`
- `stderr_does_not_contain` — same shape
- `stream_discipline_pass` — stdout JSON-parseable, stderr non-JSON

#### Provenance verbs

- `provenance_present` — `{paths?: []string}`
- `provenance_matches_cassette` — cross-checks declared sources
  against URLs recorded in the cassette

#### Judge verbs

- `judge_score_above` — `{judge_id: string, value: float 0..1}`
  Requires a matching `JudgeBlock` in the scenario.

#### Deferred verbs

- `auth_lifecycle_clean` — parsed; grader emits
  `status: not_implemented`. Wired once the auth-lifecycle harness
  lands.

### Judge blocks

```yaml
judge:
  - id: clarity-judge
    on: report          # which step's stdout feeds the prompt
    prompt: |           # required, unless prompt_ref
      score the report's clarity on a 0..1 scale
    model: claude-sonnet-4-7
    model_allowlist: [claude-sonnet-4-7, claude-opus-4-7]
```

The library ships only the `AIJudge` interface and a `Canned`
stub (`go/conformance/scenario/judge`). The production registry
(model invocation) lives in the `svc` service
(`go/conformance/svc`). Callers wire their AIJudge into
`Input.Judge`; nil + any `judge_score_above` assertion ⇒
`VerdictUngradable` with `JUDGE_UNAVAILABLE`.

When `prompt_ref` is set instead of inline `prompt`, the grader
calls `Input.JudgePromptResolver(prompt_ref)` to materialise the
prompt body. The library never reads from disk.

### Tier system

The grader emits **Tier 3** internally. The caller redacts before
surfacing via `result.ToTier(n)`:

- **Tier 1**: verdict + identifying metadata.
- **Tier 2**: + per-factor `facets[]`.
- **Tier 3**: + per-assertion `assertions[]` + `judge_traces[]`.

Identifying fields (`scenario_id`, `schema_version`, `verdict`,
`scored_at`, `grader_version`, `rules_version`, `tier`) appear at
every tier.

### Story coupling

Every scenario carries a `story_ref.content_hash` (SHA-256 of the
referenced story file's bytes). The grader hashes
`Input.StoryContent` at grade time and refuses to grade on
mismatch (`STORY_HASH_MISMATCH`, exit 4).

This prevents scenarios from drifting past their underlying user
story without an explicit re-author + rehash by the scenario
author.

### Grader exit codes

The grader's symbolic codes all map to existing kit numeric exit
codes (no new numeric codes allocated):

| Code | Exit | When |
|------|------|------|
| `SCENARIO_PARSE_ERROR` | 2 | malformed YAML / unknown key |
| `SCENARIO_VALIDATE_ERROR` | 2 | shape OK but semantically broken |
| `SCENARIO_SCHEMA_UNSUPPORTED` | 1 | binary doesn't know this schema_version |
| `GRADER_TOO_OLD` | 1 | scenario requires newer grader |
| `STORY_HASH_MISMATCH` | 4 | story bytes hash != declared |
| `JUDGE_UNAVAILABLE` | 5 | no AIJudge wired |
| `JUDGE_PROMPT_UNRESOLVED` | 5 | prompt_ref + nil resolver |
| `JUDGE_MODEL_REJECTED` | 5 | model not in allowlist |
| `JUDGE_PARSE_FAILED` | 5 | model returned bad output |
| `GRADER_INTERNAL` | 1 | grader bug |

### Grade CLI

`kit conformance grade <scenario.yaml> <cassette-dir>` is a
dev-only debug stub for local authoring round-trips. Hidden;
production graders run inside the `svc` service.

Cassette dir layout the leaf consumes:

```
<cassette-dir>/
└── steps/
    └── <step-id>/
        ├── stdout            # captured stdout (or stdout.txt / stdout.json)
        ├── stderr            # captured stderr
        ├── exit_code         # decimal integer
        └── cassette/         # xrr cassette files (optional)
```

Flags:
- `--story PATH` — story file (default: derived from `story_ref.story_path`)
- `--tier 1|2|3` — output tier (default: 3)
- `--format json|yaml` — wire format (default: json)
- `--judge-stub id=score` — repeatable canned judge scores
- `--no-judge` — disable AI judges entirely

### Adding a verb

1. Append to `contracts/scenario-rules.json` `verbs[]`.
2. Bump `rules_version` (calendar timestamp).
3. Run `make embed` (or re-`go test ./...` if embed is auto).
4. Create a new file under `verbs/` with one `Entry`:
   ```go
   func init() {
       register(&Entry{
           Kind:     KindMyNewVerb,
           Validate: validateMyNewVerb,
           Evaluate: evalMyNewVerb,
       })
   }
   ```
5. Add a constant `KindMyNewVerb` to `verbs.go`.
6. Run `go test ./go/conformance/scenario/...` — the registry
   consistency test guards against drift.

## Story DSL

`go/conformance/story/` is the Go API for kit's story DSL: the
closed-key YAML shape, parser, three-tier validator, and helpers
that scenario tooling consumes. The user-facing CLI wrapper is
`kit conformance verify-stories`; the rationale +
scenario-coupling contract lives in ADR-0026.

### What stories are

A story describes **what a user is trying to do**: plain-English
intent plus a command sequence. Stories ship in the adopter's public
repo (`e2e/stories/*.yaml`); scenarios — which carry the grading
rubric — live in a separate private repo and reference stories by
`story_id`.

Stories are deliberately structurally distinct from scenarios. They
do not carry assertions, judges, or cassette guards. The closed-key
YAML schema and the metadata-key denylist enforce this
structurally — `verify-no-leak` will never fire on a valid story.

### Package layout

| Path | Role |
|------|------|
| `schema/` | Go types + YAML tags for `Story`, `Step`, `Reference`. The closed-key set. |
| `parser/` | `ParseFile` / `ParseBytes` — decode YAML with `KnownFields(true)`. |
| `validator/` | Three-tier validator (`ValidateOne`, `ValidateAll`). |
| `toolspec/` | Minimal toolspec projection used by tier 3. |
| `story.go` (this package) | `Discover`, `Index`, `ContentSHA256`, `ReadStory`. |

### Validator tiers

| Tier | Scope | Default |
|------|-------|---------|
| 1 | Schema validity (closed-key, regex, length, enum). | always on |
| 1.5 | Metadata-key denylist (sourced from `contracts/scenario-rules.json`). | always on |
| 2 | Referential validity (uniqueness, invoke vs binary, URL parsing). | on |
| 3 | Toolspec semantic validity (every invoked command + flag must be declared). | opt-in (`--strict-toolspec`) |

A fourth tier — runtime execution — is explicitly out of scope.
Stories are validated, never executed.

### Embedding the validator

```go
import (
    "hop.top/kit/go/conformance/scenariorules"
    "hop.top/kit/go/conformance/story/parser"
    "hop.top/kit/go/conformance/story/validator"
)

doc, _ := scenariorules.LoadDefault()
ps, err := parser.ParseFile("e2e/stories/launch-dry-run.yaml")
if err != nil {
    // closed-key violations surface here as `field "X" not found`
}
findings := validator.ValidateOne(ps, validator.Options{Rules: doc})
for _, f := range findings {
    fmt.Printf("%s:%d: %s — %s\n", f.File, f.Line, f.Rule, f.Message)
}
```

### Linking from scenarios

Scenarios reference stories by `story_id`. Three coupling tiers are
recommended (the scenario grader makes the final call; see
[Story coupling](#story-coupling)):

| Tier | Scenario carries | Drift detection |
|------|------------------|------------------|
| loose | `story_id` | none |
| versioned | `story_id`, `story_schema_min` | floor only |
| strict | `story_id`, `story_schema_min`, `story_content_sha256` | full |

For strict pinning, compute the digest at authoring time:

```go
import "hop.top/kit/go/conformance/story"

s, _ := story.ReadStory("e2e/stories/launch.yaml")
digest, _ := story.ContentSHA256(s)
// store digest alongside story_id in the scenario file
```

`ContentSHA256` is stable across whitespace / comment / key-order
changes — re-marshalling through `yaml.v3` normalizes formatting
before hashing.

### Leak-rule resistance

`go/conformance/story/leak_resistance_test.go` is the live cross-
check: every reference story under `examples/spaced/e2e/stories/`
must pass both the story validator AND the leak detector on the
same bytes. A regression on either side fails the test, so the
structural-distinctness claim is enforced rather than asserted in
prose.

### Schema version policy

- `schema_version: "1"` is the only accepted value in v1.
- Additive fields within v1 are allowed in minor bumps.
- Major bump (v2) is breaking; the v2 validator will ship a
  v1-compat mode.

The wire-format JSON Schema for cross-language adopters is at
[`contracts/story-schema.json`](../../../contracts/story-schema.json).

## Related pages

- [`../guides/enforce-cli-conformance.md`](../guides/enforce-cli-conformance.md): flip `EnforceValidate` on an existing tool, step by step
- [`go-primitives.md`](go-primitives.md): every `go/conformance` package in the primitives index
- [`compliance-api.md`](compliance-api.md): the toolspec-driven `compliance` checker, a different package (`go/core/compliance`)
- [`cli-api-reference.md`](cli-api-reference.md): the annotation setters (`SetSideEffect`, `SetOutputSchema`, ...)
