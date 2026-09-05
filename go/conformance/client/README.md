# client

## What it answers

How a Go test reaches the kit conformance grading service ("svc"):
pack a cassette directory, post it, and decode the typed grading
`Result`. Pairs with the `kit conformance grade` CLI leaf. The grader
that produces the result is `go/conformance/scenario`; the local
static checker that needs no service is `go/conformance`.

## Use it when

- construct a client → `client.New(baseURL, opts...)`
- grade a cassette directory → `Client.Grade(ctx, GradeRequest{CassetteDir: ...})`
- fetch a result by grade id → `Client.Status(ctx, gradeID)`
- authenticate → `client.WithToken(t)`
- tune the retry budget or backoff → `WithMaxAttempts`, `WithBackoff`
- cap the packed body → `WithMaxCassetteSize`
- branch on a failure class → `ErrServiceUnavailable`, `ErrServiceAuthFailed`, `ErrServiceUsage`, `ErrCassettePack`, `ErrCassetteTooLarge`, `ErrManifestParse`, `ErrGradeFail`, `ErrGradeUngradable`, `ErrRateLimited`
- decide whether to re-attempt → `IsRetryable(err)`

## Quick start

```go
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
```

## Contract

- `baseURL` is required; an empty value returns `ErrServiceUsage`. There is no default kit-team-hosted endpoint.
- `Grade` packs `req.CassetteDir` deterministically, posts to `<baseURL>/v1/grade`, and returns the typed `Result`. On a 202 plus a poll URL it polls internally until 200 or the context expires.
- `Result` is a type alias to `scenario.Result`, the exact struct svc serializes under the `"result"` key, so the typed decode preserves everything the grader records, per-factor facets and per-assertion traces included.
- Exit codes follow the 12fc taxonomy: shared classes on 0-6, grade verdicts in kit's documented band above 6 (`ErrGradeFail` 68, `ErrGradeUngradable` 69, after `RATE_LIMITED` 64, `PROVENANCE_MISSING` 65, `LEAK_DETECTED` 66, `CONFIG` 67).
- Errors implement `errors.Is` for sentinel identity and `AsCLIError() *output.Error`, so kit's CLI middleware picks up the exit code automatically.
- Service-unavailable, rate-limited and transient network errors are retryable; auth, usage and grade-verdict errors are terminal.
- Cassettes pack via `Pack(dir, manifest, maxBytes)` into a deterministic gzipped tar with `manifest.yaml` at the root: the same dir yields the same bytes, the same SHA-256, and the same `Idempotency-Key`. MIME type `application/vnd.kit.cassette+tar+gzip`.

## Neighbours

- `go/conformance/scenario`: the grader and the `Result` type this package aliases.
- `go/console/cli/conformance/grade`: the `kit conformance grade` CLI leaf.
- `go/conformance/harness`: the xrr-backed toolkit that records the cassettes being graded.
- `go/conformance`: the local static checker, which needs no service.

## See also

- [Conformance reference](../../../docs/adopters/reference/conformance.md#grading-client): the constructor, every functional option and default, method semantics, the annotated `Result` fields, the full sentinel and exit-code table, and the cassette wire format
