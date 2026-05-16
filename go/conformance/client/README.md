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

Currently a structural bridge to `hop.top/kit/go/conformance/scenario.Result`
(scen track lands in parallel). The JSON wire shape is identical, so
once scen merges callers should switch to `scenario.Result` with a
compile-time assertion. The bridge fields are:

```go
type Result struct {
    ScenarioID     string         // canonical scenario name
    Verdict        string         // pass | fail | ungradable
    ExitCode       int            // suggested process-exit code
    Reason         string         // human-readable verdict summary
    Tier           int            // actually-graded tier
    ScoredAt       string         // RFC3339
    GraderVersion  string
    RulesVersion   string
    ServiceVersion string
    Facets         []Facet        // tier-2/3 factor coverage
    Findings       []Finding      // tier-3 failing assertions
    Provenance     map[string]any
}
```

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
