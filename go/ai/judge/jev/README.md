# jev

## What it answers

How to get a typed judgment (choice, score on anchors, yes / no) from
TypeSafe's Jev model through OpenRouter, with the service's own
probabilities and its USD cost on the verdict. Jev returns typed answers,
never text, so no sampling or stated confidence is involved. Always cloud:
every call sends the case's state and questions to the service. Wrong
package when you need a local or offline judge (`go/ai/judge/llmjudge`).

## Use it when

- you judge with Jev → `jev.New()`, then `Judge(ctx, c)`; key from `OPENROUTER_API_KEY` (or `jev.WithAPIKey`)
- you pick the model or endpoint → `jev.WithModel(m)` (default `typesafe/jev-1.14`), `jev.WithURL(u)`
- you test or record the HTTP → `jev.WithTransport(rt)`, any `http.RoundTripper`
- you need to know what is refused → box (image localization) questions and `Case.Assets` fail with `llmerrors.ErrCapabilityNotSupported`; Jev never abstains
- you wonder where the key goes → only the default endpoint's origin, loopback, or a llm.yaml / `LLM_BASE_URL` origin; else `New` fails with `llm.ErrKeyEndpoint`
- you read the result → `Source` is `"native"`, `Cost.USD` from `usage.cost`, yes / no `Probs` is `{yes: p, no: 1-p}`; failures arrive as llm errors or `jev.ErrMalformed`

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`jev.go`](jev.go) | `New`, options, `Judge`, HTTP and status-to-error mapping | you change the endpoint, key or error handling |
| [`wire.go`](wire.go) | request mapping, response parsing, answer building | you change how questions or answers map |
| [`jev_test.go`](jev_test.go) | hermetic tests, fake `http.RoundTripper` over the documented shapes | you add a behaviour |

## Related

- [`go/ai/judge`](../README.md)
- [`go/ai/judge/llmjudge`](../llmjudge/README.md)
- [`docs/adopters/reference/judge.md`](../../../../docs/adopters/reference/judge.md)
