# llmxrr

## What it answers

How a test runs a real provider adapter against responses a real provider
once returned, with no network and no secrets in the repo: an
`http.RoundTripper` over an xrr session, passed to `llm.WithTransport`.
Test-only; wrong package for production code.

## Use it when

- you replay recorded model calls → `llmxrr.New("testdata/cassettes")`, then `llm.Resolve(uri, llm.WithTransport(tr))`
- you record or replay → `XRR_MODE=record|replay|passthrough`; unset means replay
- you need extra values masked → `llmxrr.WithSecrets(key)`; provider env keys (`OPENAI_API_KEY`, ...) are masked already
- you test error handling → `llmxrr.AllowFailures()`; without it a transport error or non-2xx is refused (`ErrRefused`) and nothing is written
- you guard committed cassettes → `llmxrr.AssertCassettesClean(t, "testdata/cassettes", key)` in a plain test
- you need only one check → `llmxrr.CheckNoSecrets(dir, ...)`, `llmxrr.CheckNoFailures(dir)`

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`llmxrr.go`](llmxrr.go) | `Transport`, `New`, options, scrubbing, refusal | you change what a cassette keeps or refuses |
| [`scan.go`](scan.go) | `CheckNoSecrets`, `CheckNoFailures`, `AssertCassettesClean` | you add a credential shape to the leak scan |
| [`llmxrr_test.go`](llmxrr_test.go) | hermetic record/replay, scrub, refusal, scan tests | you check behaviour end to end |

## Related

- [`docs/adopters/reference/llm.md`](../../../../docs/adopters/reference/llm.md#recording-model-calls-in-tests)
