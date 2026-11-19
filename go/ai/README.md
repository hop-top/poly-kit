# ai

Packages that describe a CLI to machines and let machines act through it:
reflection, tool specifications, permission policy, LLM clients and
extension points.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`cmdreflect/`](cmdreflect/README.md) | single cobra reflector producing one `Descriptor` per command | you need a fact about a command that both the manifest and help rendering must agree on |
| [`ext/`](ext/README.md) | extension contract, manager, hook bus, discovery, dispatch, config | you add plugin points to a kit-powered tool |
| [`judge/`](judge/README.md) | typed judgments over a case: choice, score, yes/no questions; validation, batching | you need probability-bearing answers from a model or rule engine, not raw text |
| [`judge/calibrate/`](judge/calibrate/README.md) | temperature and Platt calibration, `Fit`, `Calibrated` | you calibrate a backend's probabilities |
| [`judge/judgetest/`](judge/judgetest/README.md) | deterministic fake judge | you test code that calls a judge |
| [`judge/eval/`](judge/eval/README.md) | replay a recorded case set through a candidate judge; agreement, Brier, ECE, gate | you check a judge can replace another |
| [`llm/`](llm/README.md) | provider-agnostic LLM client, routing, provider drivers | you call a model or route between providers |
| [`llm/llmxrr/`](llm/llmxrr/README.md) | test-only record/replay transport for model calls, secret scrubbing, cassette leak scan | you test code that calls a model against recorded responses |
| [`llm/strategy/`](llm/strategy/README.md) | self-consistency voting, best-of-n, cascade over a sampler; summed usage | you call a model several times and combine the answers |
| [`prompt/`](prompt/README.md) | versioned prompt templates, patterns, techniques, fragments, schema DSL | you load, render or compose prompt text for a model |
| [`toolspec/`](toolspec/README.md) | `ToolSpec` and `Manifest` data types, safety vocabulary, registry | you model a tool's commands, flags, risk and workflows |
| [`toolspec/adapters/`](toolspec/adapters/README.md) | format adapters (kit-manifest, mcp) and MCP policy gate | you render the manifest or enforce policy on an MCP call |
| [`toolspec/cli/`](toolspec/cli/README.md) | `<tool> spec` subcommand and cobra walker | your kit-powered CLI should publish its manifest |
| [`toolspec/policy/`](toolspec/policy/README.md) | side-effect x network permission table | you decide auto-allow, prompt or deny for a command |
| [`toolspec/sources/`](toolspec/sources/README.md) | help, completion, tldr, thefuck, llm, usp spec sources | you describe a tool kit does not own |

## Conventions

- `toolspec` stays pure data; I/O lives in `toolspec/sources/*` and `toolspec/cli`.
- Reflection happens once in `cmdreflect`; `toolspec/cli` and help rendering project from its descriptors.
- Sources are ordered by trust when chained; the tool's own `<tool> spec` manifest is authoritative.
- `ext/dispatch` and `ext/discover` execute binaries; import them only in binaries that mount plugins.
