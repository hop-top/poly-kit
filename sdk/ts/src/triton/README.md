# triton

## What it answers

How to call a model hosted on Triton Inference Server over HTTP with the KServe v2 protocol, including `triton://host:port/model[/version]` URI parsing. Wrong module for chat-style LLM calls (`@hop-top/kit/llm`) and for routing decisions ([`../router`](../router/README.md)).

## Use it when

- parse a configured endpoint: `parseTritonURI(uri)` returns `{ host, port, modelName, modelVersion }`
- run inference: `new TritonClient(...)` implementing `TritonScorer`
- liveness: `ServerHealth`

## Quick start

```ts
import { parseTritonURI } from './client';

console.log(parseTritonURI('triton://localhost:8000/bert_router'));
```

## Contract

- Request and response bodies are the KServe v2 `InferRequest` / `InferResponse` shapes; tensor `datatype` is one of `TritonDataType`.
- Failures raise `TritonError`.
- Not on the package exports map and not in the `tsup` build: reachable only by relative import inside `src/`.

## Neighbours

- [`../router/bert.ts`](../router/bert.ts): the consumer, through the `TritonInfer` interface
- `@hop-top/kit/llm`: provider clients for chat completions

## See also

- [KServe v2 inference protocol](https://kserve.github.io/website/modelserving/inference_api/)
