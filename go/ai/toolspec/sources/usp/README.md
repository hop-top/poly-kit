# usp

## What it answers

Which commands an operator actually runs after which, learned from Bash and
shell tool calls in their own agent sessions and emitted as workflows (User
Session Patterns). Wrong package for published examples
(`hop.top/kit/go/ai/toolspec/sources/tldr`).

## Use it when

- you wire a session store: `usp.NewUSPSource(usp.Config{Adapter: a, CWD: dir, MinCount: 2, MaxSessions: 50})`
- you want incremental rescans: `ResolveIncremental`, `LoadTransitions`, `CachedTransitions` for persisting the `TransitionMap`
- you work on the pieces directly: `Extract` (tool calls to `ParsedCommand`), `CountTransitions`, `BuildSpec`, `DetectCrossToolWorkflows` with `DefaultPatterns`

## Contract

- Reads: sessions listed by the `SessionAdapter` for `CWD`; only `Bash` and `shell` tool calls are parsed. Consumers supply the adapter; this package has no store of its own.
- Trust: medium. Data is local and factual, but a transition is a habit, not a requirement; `MinCount` (default 2) prunes noise and `MaxSessions` (default 50) bounds the scan.
- Cross-tool workflows come from named patterns (for example `pr-merge-flow`); duplicate workflow names are skipped on merge.
- `Resolve` returns `nil, nil` when no adapter is set or no workflow reaches `MinCount`; results are cached per tool.
- No snippet here: the source reads operator session data through an adapter.

## Neighbours

- `hop.top/kit/go/ai/toolspec/sources/tldr`: workflows from published examples.
- `hop.top/kit/go/ai/toolspec`: `Workflow`, `Merge`.

## See also

- [ToolSpec API reference](../../../../../docs/adopters/reference/toolspec-api.md)
