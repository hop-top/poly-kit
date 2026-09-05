# adapters

One `InvocationAdapter` per agent CLI, each translating the universal
`invoke.Invocation` into that binary's argv.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`claude/`](claude/README.md) | `claude` (Claude Code) | you launch Claude Code |
| [`codex/`](codex/README.md) | `codex` (OpenAI Codex CLI) | you launch Codex |
| [`copilot/`](copilot/README.md) | `copilot` (GitHub Copilot CLI) | you launch Copilot |
| [`crush/`](crush/README.md) | `crush` (Charm Crush) | you launch Crush |
| [`cursoragent/`](cursoragent/README.md) | `cursor-agent` | you launch Cursor's agent |
| [`gemini/`](gemini/README.md) | `gemini` (Gemini CLI) | you launch Gemini |
| [`goose/`](goose/README.md) | `goose` (Block Goose) | you launch Goose |
| [`kimi/`](kimi/README.md) | `kimi` (Kimi Code CLI) | you launch Kimi |
| [`opencode/`](opencode/README.md) | `opencode` | you launch OpenCode |
| [`qwen/`](qwen/README.md) | `qwen` (Qwen Code) | you launch Qwen |
| [`vibe/`](vibe/README.md) | `vibe` (Mistral Vibe) | you launch Vibe |

## Conventions

- `New()` returns the adapter; `Binary` is the executable name `CommandSpec.Path` carries.
- `Mappings()` covers every universal option; the parity matrix in `go/core/uxp/README.md` is generated from it (`go generate ./go/core/uxp/...`).
- Shims come only from `../shim`; an adapter never invents its own.
- Each README records the CLI version and help capture it was verified against; re-verify when the surface changes.
