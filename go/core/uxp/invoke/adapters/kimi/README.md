# kimi adapter

Invocation adapter for Kimi Code CLI (`kimi` binary, Moonshot AI).

## Last verified

- Date: 2026-05-09
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/kimi.txt`

## Distinctive shape

- **Native `--plan` flag** for ApprovalPlan — kimi is one of the few non-claude/non-gemini/non-qwen CLIs with first-class plan-mode support.
- **Two auto-all variants**: `--yolo` (immediate auto-accept) and `--afk` (away-from-keyboard mode that auto-dismisses AskUserQuestion). Adapter maps `ApprovalAutoAll` to `--yolo`; AFK is exposed as `kimi.afk` Config key.
- `--output-format` choices are `text` and `stream-json` only — no `json`. `OutputJSON` shims via `--print --output-format text --final-message-only` (alias `--quiet`) which prints the final assistant message as plain text.
- Native `--agent default|okabe` builtins plus `--agent-file FILE` for custom agents.
- Native `-w/--work-dir <dir>` for CWD.
- No per-invocation sandbox flag.

## Mapping summary

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `--print` | |
| `ModeInteractive` | (default) | |
| `ModeResume` | `-S/--session <id>` | |
| `Continue` | `-C/--continue` | |
| `Fork` | unsupported | |
| `CWD` | `-w/--work-dir <dir>` | also set on `CommandSpec.Dir` |
| `Model` | `-m/--model` | |
| `Agent` | `--agent default\|okabe` | + `--agent-file <FILE>` via Config |
| `OutputJSON` | `--print --output-format text --final-message-only` (shim) | no json choice; final-message text |
| `OutputStreamJSON` | `--output-format stream-json` | |
| `SandboxReadOnly` / `WorkspaceWrite` / `DangerFullAccess` | unsupported | no sandbox flag |
| `ApprovalAsk` | (default) | |
| `ApprovalPlan` | `--plan` | native! |
| `ApprovalAutoEdit` | unsupported | refused (`--yolo`/`--afk` are auto-all only) |
| `ApprovalAutoAll` | `--yolo` | **dangerous**; opt-in required |
| `ApprovalNever` | unsupported | |
| `AddDirs` | `--add-dir <dir>` (repeatable) | |
| `Files` | S-1 + S-3 | |
| `Images` | S-3 | |

## Recognized Config keys (`kimi.*` namespace)

| Key | Native flag | Type |
|---|---|---|
| `kimi.thinking` | `--thinking` / `--no-thinking` | bool |
| `kimi.afk` | `--afk` | bool — auto-all variant; auto-dismisses AskUserQuestion |
| `kimi.agent_file` | `--agent-file <FILE>` | string |
| `kimi.skills_dir` | `--skills-dir <DIR>` (repeatable) | comma-list |
| `kimi.max_steps_per_turn` | `--max-steps-per-turn` | string-numeric |
| `kimi.max_retries_per_step` | `--max-retries-per-step` | string-numeric |
| `kimi.max_ralph_iterations` | `--max-ralph-iterations` | string-numeric (-1 = unlimited) |
| `kimi.mcp_config` | `--mcp-config` (repeatable) | comma-list of inline JSON |
| `kimi.mcp_config_file` | `--mcp-config-file` (repeatable) | comma-list of file paths |
| `kimi.config_file` | `--config-file` | path |

## Notes

- `kimi acp` / `kimi term` / `kimi vis` / `kimi web` / `kimi mcp` are management subcommands, not invocation paths; adapter does not expose them.
- `--prompt`/`-p` is required even for non-interactive runs (kimi reads the prompt from this flag, not stdin or positional). Adapter always emits `--prompt <text>`.
- `--input-format text|stream-json` for streaming user input is not exposed as a universal option; pass via `ExtraArgs` if a caller needs streaming inputs.
