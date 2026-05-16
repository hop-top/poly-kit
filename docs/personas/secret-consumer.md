---
id: secret-consumer
name: "Secret Consumer"
role: "Developer reading secrets in kit-based apps"
extends: go-toolmaker
languages: [go, typescript, python]
---

## Context

Application developer who needs credentials (API keys, DB
passwords, tokens) at runtime. Does not manage the secret
infrastructure -- just reads values through kit's unified API.

## Needs

- Simple `Get(ctx, key)` API; no backend-specific code
- Multiple backends swappable without app changes
- Local dev works without production infra (env vars or files)
- No vendor lock-in; swap OpenBao for Infisical without refactor
- Cross-language access (Go native, TS/Python via kit serve)

## Pain points

- Each secret provider has its own SDK, auth flow, error semantics
- Env vars don't support rotation or metadata
- Testing with real backends is slow and flaky
- 12-factor apps force env-only; can't use encrypted files easily
- No standard way to check if a secret exists before reading

## Success criteria

- 3 lines to read a secret in any supported language
- `memory.New()` in tests; `env.New()` in CI; same app code
- Swap backend via config, zero code change
- `secret.ErrNotFound` is the only error to handle for missing keys

## Status

Planned. Will activate when the secret-management workstream lands
its first cross-language story exercising `secret.Get(ctx, key)` in
Go, TS, and Python. Until then, no inbound references in this repo;
audience scope retained so the eventual story (and its toolspec /
parity tests) can target this persona without a re-draft.

Trigger to activate: a story that adds a `secret.*` API surface to
kit and at least two of the three SDKs (sdk/go, sdk/ts, sdk/py).
At that point, link this file from the story's frontmatter and
remove this status block.
