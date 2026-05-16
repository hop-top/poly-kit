# opencode adapter

Invocation adapter for opencode CLI.

## Last verified

- Date: 2026-05-09
- Binary: `opencode` 1.14.30
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/opencode.txt`
  (top-level + `run` subcommand)

## Distinctive shape

opencode is the only adapter where the universal **AddDirs** has no
native flag but **Files** does. The S-2 shim (`enumerateDirFiles`)
inverts the usual direction: `AddDirs` walks each directory and emits
one `--file` argument per file.

This is also the case the spec brainstorm specifically called out:
*"takes --files but not dir → list files to add"*.

## Mode → subcommand routing

| Mode | Subcommand |
|---|---|
| `ModeInteractive` | (none — bare `opencode [project]`) |
| `ModeRun` | `opencode run [message..]` |
| `ModeResume` | `opencode run --session <id>` (or `--continue`) |
| `ModeResume + Fork` | `--fork` modifier on the resume |

`opencode resume` is not a separate subcommand — resume is a flag set
on `opencode run`.

## Mapping summary

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `run [message..]` | |
| `ModeResume` | `run --session <id>` | |
| `Continue` | `run --continue` | |
| `Fork` | `--fork` | with `--continue` or `--session` |
| `CWD` | `--dir <dir>` | for run/resume; bare TUI uses positional |
| `Model` | `-m/--model` | accepts `provider/model` |
| `Agent` | `--agent <name>` | |
| `OutputJSON` | `--format json` (shim) | opencode emits JSONL event stream; caller must reduce to final assistant message |
| `OutputStreamJSON` | `--format json` | native event stream |
| `SandboxReadOnly` | unsupported | no per-tier sandbox |
| `SandboxWorkspaceWrite` | unsupported | |
| `SandboxDangerFullAccess` | `--dangerously-skip-permissions` | **dangerous**; opt-in required |
| `ApprovalAsk` | (default) | |
| `ApprovalPlan` | unsupported | |
| `ApprovalAutoEdit` | unsupported | refused per anti-shim |
| `ApprovalAutoAll` | `--dangerously-skip-permissions` | **dangerous**; opt-in required |
| `ApprovalNever` | unsupported | |
| `AddDirs` | **S-2** (enumerate → `--file`) | shim — opencode has no `--add-dir` |
| `Files` | `-f/--file <path>` (repeatable) | |
| `Images` | `-f/--file <path>` (shim) | opencode does not distinguish image attachments |

## Shims invoked

- **S-2 (`enumerateDirFiles`)** — for each `Invocation.AddDirs` entry, walks the directory and emits `--file <path>` per regular file. Honors `Config["uxp.shim.dir_to_files_max"]` (default 200). Files already in `Invocation.Files` are filtered out to avoid double-listing. Overflow → **hard error** with diagnostic listing the offending dir and cap.
- **S-3 not used** — opencode accepts files natively; the prompt-block fallback is unnecessary. The composed prompt is just `Invocation.Prompt`.

## Anti-shims (refused mappings)

- `Sandbox = SandboxReadOnly` or `SandboxWorkspaceWrite` → **error**. opencode has no per-tier sandbox; configure via `opencode config` instead.
- `Sandbox = SandboxDangerFullAccess` without `Config["uxp.allow_dangerous"]="true"` → **error**.
- `Approval = ApprovalAutoEdit` → **error**. No native auto-edit; refuses to degrade to dangerous bypass.
- `Approval = ApprovalAutoAll` without opt-in → **error**.
- `Approval = ApprovalPlan` or `ApprovalNever` → **error**. No native equivalent.
- `Fork = true` outside `Mode = ModeResume` → **error**.
- `Mode = ModeResume` without `SessionID` and without `Continue = true` → **error**.

## Recognized Config keys (`opencode.*` namespace)

| Key | Native flag | Type |
|---|---|---|
| `opencode.variant` | `--variant` | string (provider-specific reasoning effort: high, max, minimal) |
| `opencode.thinking` | `--thinking` | bool — show thinking blocks |
| `opencode.share` | `--share` | bool — share the session |
| `opencode.title` | `--title` | string — explicit session title |
| `opencode.command` | `--command` | string — the command to run, message becomes args |
| `opencode.attach` | `--attach` | string — running server URL |
| `opencode.password` | `--password` | string — basic auth (prefer `OPENCODE_SERVER_PASSWORD` env) |
| `opencode.port` | `--port` | string — local server port |
| `opencode.pure` | `--pure` | bool — skip external plugins |

Universal `Config["uxp.shim.dir_to_files_max"]` controls the S-2 cap (default 200).

## Notes

- opencode's distinctive `provider/model` model spec (e.g. `anthropic/sonnet-4-6`) is honored as-is by the adapter; `Model` carries the full string.
- Web search is plugin-only; the tools.go entry marks it `MappingShim` because it depends on which plugins/MCP servers the user has configured.
- opencode's `task` subcommand spawns subagents but is not a flag-level surface — it's an in-conversation tool the agent uses, recorded in `ToolCapabilities()` but not in `Mappings()`.
- A future `opencode/output.go` helper could implement the JSONL → final-message reducer for `OutputJSON`. For now, callers consume the diagnostic and write their own reducer.
