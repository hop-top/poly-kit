# triton

## What it answers

How a Rust tool scores a float vector on an NVIDIA Triton Inference
Server: the KServe v2 inference protocol over HTTP REST (as Go
`ai/llm/triton`; no gRPC, no protobuf). Registers `triton` in
`default_registry()`. Feature `llm-triton`, native only.

## Use it when

- you need a scalar from a model (a routing classifier) → `Client::new(cfg)?` then `Scorer::score(&input)`
- you resolve `triton://model` or `triton://host:port/model` → base URL defaults to `http://localhost:8000`
- a gateway in front of Triton checks a key → `TRITON_API_KEY` (optional, `Authorization: Bearer`)

## Contract

- Request: `POST <base>/v2/models/<model>/infer` with one input `input`, shape `[1, n]`, `FP32`. The score is the first value of the first output.
- Errors: non-2xx is `HttpStatus` (5xx, fallbackable) or `Api`, text `triton: HTTP <status>: <body>`; no outputs or empty data is `Protocol`. Not retried, redirects not followed, credentials scrubbed, an unparseable base URL rejected at build with credentials masked.
- `Client` is a `Provider` with no capabilities, as in Go.

## See also

- [`go/ai/llm/triton`](../../../../../go/ai/llm/triton), the Go reference
