# qwen adapter

Invocation adapter for Qwen Code CLI (`qwen` binary).

## Last verified

- Date: 2026-05-09
- Binary: `qwen` 0.15.6
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/qwen.txt`

## Distinctive shape

- **Closest gemini peer.** Output formats and approval modes are nearly identical, but qwen has full `--approval-mode plan|default|auto-edit|yolo` parity *and* native `-y/--yolo`.
- **Positional prompt over `-p`.** qwen documents `-p/--prompt` as deprecated; the adapter passes the prompt as the trailing positional arg.
- `--include-directories` and `--add-dir` are aliases of the same flag; the adapter emits `--include-directories` for clarity.
- `-s/--sandbox` is boolean (like gemini), not tier-valued.

## Mapping summary

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `qwen [query..]` | positional prompt |
| `ModeInteractive` | `qwen [query..]` | or `-i/--prompt-interactive` for hybrid |
| `ModeResume` | `-r/--resume <id>` | |
| `Continue` | `-c/--continue` | |
| `Fork` | unsupported | |
| `CWD` | `CommandSpec.Dir` | |
| `Model` | `-m/--model` | |
| `Agent` | unsupported | |
| `OutputJSON` | `-o/--output-format json` | |
| `OutputStreamJSON` | `-o/--output-format stream-json` | |
| `SandboxReadOnly` | `--approval-mode plan` (shim) | |
| `SandboxWorkspaceWrite` | `-s/--sandbox` boolean (shim) | |
| `SandboxDangerFullAccess` | `-y/--yolo` | **dangerous**; opt-in required |
| `ApprovalPlan` | `--approval-mode plan` | full parity |
| `ApprovalAutoEdit` | `--approval-mode auto-edit` | full parity |
| `ApprovalAutoAll` | `--approval-mode yolo` | **dangerous**; opt-in required |
| `ApprovalNever` | unsupported | |
| `AddDirs` | `--include-directories <dir>` (repeatable) | |
| `Files` | S-1 + S-3 | parent-dir reduce → `--include-directories`; prompt-block |
| `Images` | S-3 | prompt-block |

## Recognized Config keys (`qwen.*` namespace)

| Key | Native flag | Type |
|---|---|---|
| `qwen.system_prompt` | `--system-prompt` | string |
| `qwen.append_system_prompt` | `--append-system-prompt` | string |
| `qwen.max_session_turns` | `--max-session-turns` | string-numeric |
| `qwen.session_id` | `--session-id` | string |
| `qwen.chat_recording` | `--chat-recording` | bool |
| `qwen.allowed_tools` | `--allowed-tools` (repeatable) | comma-list |
| `qwen.allowed_mcp_server_names` | `--allowed-mcp-server-names` (repeatable) | comma-list |
| `qwen.core_tools` | `--core-tools` (repeatable) | comma-list of tool paths |
| `qwen.exclude_tools` | `--exclude-tools` (repeatable) | comma-list |
| `qwen.extensions` | `-e/--extensions` (repeatable) | comma-list |
| `qwen.auth_type` | `--auth-type` | one of `openai\|anthropic\|qwen-oauth\|gemini\|vertex-ai` |
| `qwen.channel` | `--channel` | one of `VSCode\|ACP\|SDK\|CI` |
| `qwen.openai_api_key` | `--openai-api-key` | string |
| `qwen.openai_base_url` | `--openai-base-url` | string |
| `qwen.screen_reader` | `--screen-reader` | bool |
| `qwen.bare` | `--bare` | bool — minimal mode, skips startup auto-discovery |

## Notes

- `--telemetry-*` and `--proxy` flags are documented as deprecated in favor of `settings.json`; not exposed via Config.
- `--checkpointing` is also deprecated; configure via settings.
- `--input-format` (text|stream-json), `--json-fd`, `--json-file`, `--input-file` are advanced bidirectional-IO surfaces — pass via `ExtraArgs` if needed.
