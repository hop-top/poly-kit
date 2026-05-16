# cursor-agent adapter

Invocation adapter for Cursor Agent CLI (`cursor-agent` binary).

## Last verified

- Date: 2026-05-09
- Binary: `cursor-agent` 2025.10.01-f425367
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/cursor-agent.txt`

## Distinctive shape

- `--output-format text|json|stream-json` works **only with `--print`**. Non-text formats with `Mode = ModeInteractive` are refused.
- `cursor-agent sandbox` is a *config* subcommand (enable/disable/reset), not a per-invocation flag. All `Sandbox*` tier options are refused; only `SandboxDangerFullAccess` maps via `-f/--force` (which is auto-all in approval terms too).
- AddDirs / Files / Images all lack flags → all three go through the prompt-block (S-3 with a workspace-directories preamble).
- Continue (resume latest) uses the `resume` *subcommand*, not `--resume` flag.

## Mapping summary

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `-p/--print` | |
| `ModeInteractive` | (default) | |
| `ModeResume` | `--resume <id>` | |
| `Continue` | `resume` (subcommand) | |
| `Fork` | unsupported | |
| `CWD` | `CommandSpec.Dir` | |
| `Model` | `--model` | |
| `Agent` | unsupported | |
| `OutputJSON` | `--output-format json` | requires `--print` |
| `OutputStreamJSON` | `--output-format stream-json` | requires `--print` |
| `SandboxReadOnly` / `SandboxWorkspaceWrite` | unsupported | sandbox is a config subcommand |
| `SandboxDangerFullAccess` | `-f/--force` | **dangerous**; opt-in required |
| `ApprovalAsk` | (default) | |
| `ApprovalPlan` / `ApprovalNever` | unsupported | |
| `ApprovalAutoEdit` | unsupported | refused (`-f` is auto-all) |
| `ApprovalAutoAll` | `-f/--force` | **dangerous**; opt-in required |
| `AddDirs` | S-3 | prompt-block with workspace dirs preamble |
| `Files` | S-3 | prompt-block listing |
| `Images` | S-3 | prompt-block listing |

## Recognized Config keys (`cursor.*` namespace)

| Key | Native flag | Type |
|---|---|---|
| `cursor.api_key` | `--api-key <key>` | string (prefer `CURSOR_API_KEY` env) |
| `cursor.background` | `-b/--background` | bool |
| `cursor.stream_partial_output` | `--stream-partial-output` | bool — only with stream-json |

## Notes

- `cursor-agent sandbox enable|disable|reset|run` is a separate management surface; the adapter does not expose those subcommands.
- `cursor-agent ls` and `cursor-agent create-chat` are session-management commands handled by usp, not the invocation facade.
- The TUI's `--background` mode opens a composer picker on launch — useful for shell scripts that pre-stage prompts but want manual review.
