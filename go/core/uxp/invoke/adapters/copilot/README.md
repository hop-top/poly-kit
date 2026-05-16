# copilot adapter

Invocation adapter for GitHub Copilot CLI (`copilot` binary).

## Last verified

- Date: 2026-05-09
- Binary: GitHub Copilot CLI 1.0.15
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/copilot.txt`

## Distinctive shape

- `--output-format json` is **JSONL** (one object per line). The adapter shims `OutputJSON` (caller must reduce to final assistant message); `OutputStreamJSON` is the same flag but treated as native because the universal contract for stream-json is "events to stdout".
- Non-interactive runs (`-p`) require **some auto-approve signal** or copilot will block on the first tool prompt and exit. The adapter emits a `warning` diagnostic when `Mode = ModeRun` and none of `--yolo` / `--allow-all` / `--allow-all-tools` / `--allow-tool=...` / `--autopilot` is in argv or Config. Callers can opt in via `Approval = ApprovalAutoAll` (with `uxp.allow_dangerous`), `Sandbox = SandboxDangerFullAccess` (with same opt-in), or any `copilot.allow_*` Config key.
- Rich tool/url policy DSL exposed via `copilot.allow_tool`, `copilot.deny_tool`, `copilot.allow_url`, `copilot.deny_url`. Repeatable values use comma-joined strings (S-shim's `SplitConfigList`).

## Mapping summary

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `-p/--prompt <text>` | |
| `ModeInteractive` | (default) or `-i <prompt>` | `-i` for interactive-with-initial-prompt |
| `ModeResume` | `--resume=<id>` | |
| `Continue` | `--continue` | |
| `Fork` | unsupported | |
| `CWD` | `CommandSpec.Dir` | no `--cd`/`--dir` flag |
| `Model` | `--model` | |
| `Agent` | `--agent` | |
| `OutputJSON` | `--output-format json` (shim) | JSONL stream; reduce required |
| `OutputStreamJSON` | `--output-format json` | native |
| `SandboxReadOnly` / `SandboxWorkspaceWrite` | unsupported | use tool/url policy |
| `SandboxDangerFullAccess` | `--yolo` | **dangerous**; opt-in required |
| `ApprovalAsk` | (default) | |
| `ApprovalPlan` / `ApprovalNever` | unsupported | |
| `ApprovalAutoEdit` | unsupported | refused per anti-shim |
| `ApprovalAutoAll` | `--yolo` | **dangerous**; opt-in required |
| `AddDirs` | `--add-dir <directory>` | repeatable, native |
| `Files` | S-1 + S-3 | parent-dir reduce → `--add-dir`; prompt-block |
| `Images` | S-3 (prompt-block) | no headless image flag |

## Recognized Config keys (`copilot.*` namespace)

Tool policy:
- `copilot.allow_tool`, `copilot.deny_tool` — repeatable comma-list (e.g. `shell(git:*),write`)
- `copilot.allow_url`, `copilot.deny_url` — repeatable
- `copilot.allow_all`, `copilot.allow_all_tools`, `copilot.allow_all_paths`, `copilot.allow_all_urls`, `copilot.yolo` — booleans
- `copilot.available_tools`, `copilot.excluded_tools` — string (comma-separated for the model's view)
- `copilot.no_ask_user` — bool
- `copilot.no_custom_instructions` — bool
- `copilot.disable_builtin_mcps` — bool
- `copilot.disable_mcp_server` — repeatable comma-list
- `copilot.additional_mcp_config` — JSON string or `@file` path

Behavior tuning:
- `copilot.autopilot` — bool — enables `--autopilot` continuation in prompt mode
- `copilot.max_autopilot_continues` — string-numeric
- `copilot.effort` — `low|medium|high|xhigh`
- `copilot.stream` — `on|off`
- `copilot.log_level` — `none|error|warning|info|debug|all|default`

Output / sharing:
- `copilot.share` — bool (`--share`) or string (`--share=path`)
- `copilot.share_gist` — bool
- `copilot.secret_env_vars` — comma-list of env var names to redact
