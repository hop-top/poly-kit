# router

## What it answers

How a prompt is scored and sent to a strong or a weak model: the RouteLLM `Router` interface, the scoring routers (`random`, `mf`, `sw`, `bert`), the `Controller` that owns thresholds and middleware, and a Hono handler exposing `/v1/chat/completions`. Wrong module for calling a RouteLLM server from a client (`@hop-top/kit/routellm` registers the `routellm://` URI scheme) and for provider clients (`@hop-top/kit/llm`).

## Use it when

- pick a model for a prompt in-process: `new Controller({ strongModel, weakModel, routers })` then `controller.route(prompt, routerName, threshold)`
- add a scorer: implement `Router.score(prompt): Promise<number>` in `[0, 1]`
- pre-route by intent: `IntentModelSelector` middleware in [`intent.ts`](intent.ts)
- serve OpenAI-compatible routing: [`server.ts`](server.ts)

## Quick start

```ts
import { Controller } from './controller';
import { RandomRouter } from './random';

const controller = new Controller({
  strongModel: 'gpt-4',
  weakModel: 'gpt-3.5-turbo',
  routers: { random: new RandomRouter() },
});
console.log(await controller.route('hello', 'random', 0.5));
```

## Contract

- `score` returns the strong-model win-rate estimate; `score >= threshold` selects `pair.strong`, else `pair.weak`.
- Server model names use the `router-<name>-<threshold>` form parsed by `parseModelName`.
- Not on the package exports map and not in the `tsup` build: reachable only by relative import inside `src/`.
- `bert` scores through the KServe client in [`../triton`](../triton/README.md); `mf` and `sw` take an embedding function.

## Neighbours

- `@hop-top/kit/routellm`: client-side `routellm://name:threshold` adapter
- `@hop-top/kit/llm`: provider registry and `Client` facade
- [`../triton`](../triton/README.md): inference transport for `bert`
- Go: `go/console/cli/router`, the `kit llm router` subcommand tree

## See also

- [`docs/adopters/reference/go-primitives.md`](../../../../docs/adopters/reference/go-primitives.md)
