# sources

Sub-packages that populate a `toolspec.ToolSpec` from outside the tool's own
cobra tree; the question each answers is "where does this fact about the tool
come from, and how far can a harness trust it".

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`completion/`](completion/README.md) | static parse of bash or zsh completion scripts; trust: medium, no exec | you have the completion script but not the binary |
| [`help/`](help/README.md) | runs `<tool> --help`, parses sections; trust: medium, executes the tool | you need commands and flags for a binary on PATH |
| [`llm/`](llm/README.md) | asks an LLM for commands, error patterns, workflows; trust: low, generated | nothing else covers the tool and a `llm.Completer` is wired |
| [`thefuck/`](thefuck/README.md) | static parse of thefuck Python rules into error patterns; trust: medium, heuristic | you want error-to-fix mappings without running anything |
| [`tldr/`](tldr/README.md) | static parse of tldr-pages markdown into workflows; trust: medium, community content | you want example workflows for a well-known tool |
| [`usp/`](usp/README.md) | learns workflows from the operator's own agent sessions; trust: medium, local data | you want workflows that reflect how this operator actually uses the tool |

## Conventions

- Every source implements `toolspec.Source` (`Resolve(tool) (*ToolSpec, error)`) or exposes pure `Parse*` functions the caller feeds.
- Order sources by trust when chaining: `toolspec.NewRegistry(toolspec.WithSource(...))` and `toolspec.ChainSources` merge in order, earlier sources win, later ones only fill empty fields.
- Sources that execute anything (`help`, `llm`, `usp`) return `nil, nil` when they have nothing; `Registry.Resolve` still returns an empty spec named after the tool.
- Generated or heuristic data carries `Provenance` or `Confidence` on `ErrorPattern` and `Workflow`; harnesses should surface it rather than treat all fields alike.
- The manifest a kit-powered tool emits via `<tool> spec` is authoritative; sources are for tools kit does not own.
