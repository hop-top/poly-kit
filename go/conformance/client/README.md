# `hop.top/kit/go/conformance/client`

Go library for the hop.top/kit conformance grading service ("svc").
Pairs with the `kit conformance grade` CLI leaf (see
`go/console/cli/conformance/grade/`).

## Quick start

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

## Constructor

```go
func New(baseURL string, opts ...Option) (*Client, error)
```

baseURL is required; an empty value returns `ErrServiceUsage` — there
is no default kit-team-hosted endpoint.

## Functional options

| Option | Default | Purpose |
|--------|---------|---------|
| `WithToken(t)` | "" | bearer token for the Authorization header |
| `WithHTTPClient(h)` | `http.Client{}` | inject a pre-configured client |
| `WithUserAgent(ua)` | `kit-conformance-client/<ver>` | override User-Agent |
| `WithMaxAttempts(n)` | 3 | retry budget (1 = no retries) |
| `WithBackoff(init, max, mult, jitter)` | 500ms, 10s, 2.0, 0.3 | retry backoff |
| `WithMaxCassetteSize(n)` | 50 MiB | packed cassette body cap |

## Methods

```go
func (c *Client) Grade(ctx context.Context, req GradeRequest) (*Result, error)
func (c *Client) Status(ctx context.Context, gradeID string) (*Result, error)
```

`Grade` packs `req.CassetteDir` deterministically, posts to
`<baseURL>/v1/grade`, and returns the typed Result. If svc responds
with 202 + a poll URL, Grade polls internally until 200 or ctx
expires. `Status` fetches a result by grade-id.

## Result type

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

## Error envelope

The package exports typed sentinels matching the kit conformance
sentinel pattern:

| Sentinel | Exit | Meaning |
|----------|------|---------|
| `ErrServiceUnavailable` | 4 | retry-budget exhausted on 5xx / network |
| `ErrServiceAuthFailed` | 5 | 401/403 from svc |
| `ErrServiceUsage` | 3 | 4xx other than 401/403/429 |
| `ErrCassettePack` | 5 | local pack failure |
| `ErrCassetteTooLarge` | 3 | body > `WithMaxCassetteSize` |
| `ErrManifestParse` | 3 | manifest.yaml could not be read |
| `ErrGradeFail` | 2 | verdict=fail |
| `ErrGradeUngradable` | 2 | verdict=ungradable |
| `ErrRateLimited` | 4 | 429 with no headroom |

Errors implement `errors.Is` for sentinel identity and
`AsCLIError() *output.Error` so kit's CLI middleware picks up the
exit code automatically.

`IsRetryable(err)` reports whether an error should be re-attempted
by the retry loop. Service-unavailable + rate-limited + transient
network errors are retryable; auth/usage/grade-verdict errors are
terminal.

## Cassette wire format

Packed via `Pack(dir, manifest, maxBytes)`. Deterministic gzipped tar
with manifest.yaml at the root; same dir → same bytes → same
SHA-256 → same `Idempotency-Key`.

MIME type: `application/vnd.kit.cassette+tar+gzip`.
