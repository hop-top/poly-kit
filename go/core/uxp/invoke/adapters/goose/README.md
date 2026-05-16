# goose adapter

Invocation adapter for goose CLI (Block, Inc.).

## Last verified

- Date: 2026-05-09
- Binary: `goose` 1.33.1
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/goose.txt` (top-level + `goose run` + `goose session`)

## Distinctive shape

- **Subcommand routing**: `goose run` for headless, `goose session` for interactive (and forked-from-resume).
- **Native fork** via `goose session --resume --fork` — only non-claude/codex/opencode CLI besides cursor's create-chat that has true fork.
- **S-5 recipe shim**: `Invocation.Agent` maps to `--recipe <name>`. Goose recipes are richer than the universal Agent concept (params, sub-recipes, system prompt overrides), so additional Config keys (`goose.recipe_params`, `goose.sub_recipe`) extend the shim.
- **Full output-format parity**: `text`, `json`, `stream-json`.
- **No per-invocation sandbox or approval**: configured globally via `goose configure`. Only `--container <id>` for extension-level isolation, exposed as `goose.container` Config.
- **No CWD flag**: process cwd via `Dir`.
- **No `--add-dir`, no per-file flag, no image flag** → all three universal options shim to S-3 prompt-block.

## Mapping summary

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `goose run -t <text>` | |
| `ModeInteractive` | `goose session` | |
| `ModeResume` | `goose run --resume --session-id <id>` | |
| `Continue` | `goose run --resume` | no `--session-id` |
| `Fork` | `goose session --resume --fork` | uses `session`, not `run` |
| `CWD` | `CommandSpec.Dir` | no `--cd` flag |
| `Model` | `--model <name>` | per-run override |
| `Agent` | `--recipe <name>` (S-5) | shim with diagnostic |
| `OutputJSON` | `--output-format json` | |
| `OutputStreamJSON` | `--output-format stream-json` | |
| `Sandbox*` | unsupported | global config or `--container` |
| `ApprovalAsk` | (default) | |
| `ApprovalPlan` / `AutoEdit` / `AutoAll` / `Never` | unsupported | global config |
| `AddDirs` / `Files` / `Images` | S-3 (prompt-block) | |

## Recognized Config keys (`goose.*` namespace)

| Key | Native flag | Type |
|---|---|---|
| `goose.provider` | `--provider` | string (override `GOOSE_PROVIDER` env) |
| `goose.name` | `-n/--name <name>` | session name |
| `goose.session_id` | `--session-id` | UUID — only emitted if not already set by resume routing |
| `goose.system` | `--system <text>` | additional system instructions |
| `goose.max_turns` | `--max-turns` | string-numeric |
| `goose.max_tool_repetitions` | `--max-tool-repetitions` | string-numeric |
| `goose.container` | `--container <id>` | container id for sandboxed extension execution |
| `goose.with_extension` | `--with-extension` (repeatable) | comma-list of `'ENV1=v COMMAND ARGS'` strings |
| `goose.with_streamable_http_extension` | `--with-streamable-http-extension` (repeatable) | comma-list of URLs |
| `goose.with_builtin` | `--with-builtin <names>` | comma-separated builtin extension names |
| `goose.no_profile` | `--no-profile` | bool — skip default extensions |
| `goose.no_session` | `--no-session` | bool — execute without creating a session file |
| `goose.quiet` | `--quiet` / `-q` | bool — print only model response to stdout |
| `goose.recipe_params` | `--params KEY=VALUE` (repeatable) | comma-list of `key=value` pairs |
| `goose.sub_recipe` | `--sub-recipe` (repeatable) | comma-list of sub-recipe names or paths |

## Notes

- `goose acp` / `goose serve` / `goose schedule` / `goose gateway` are management subcommands, not invocation paths.
- `goose mcp` is for managing MCP servers; per-run MCP attachment goes via `--with-extension`.
- `--render-recipe` and `--explain` are recipe inspection commands, not invocation paths.
- `goose project` / `goose projects` are project-directory shortcuts; not exposed.
