# crush adapter

Invocation adapter for Crush CLI (Charmbracelet).

## Last verified

- Date: 2026-05-09
- Binary: `crush` v0.65.2
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/crush.txt`

## Distinctive shape (= leanest in the suite)

- **No output-format flag.** Both `OutputJSON` and `OutputStreamJSON` are refused with a hard error.
- **No fork.** `--session` resumes; no flag to fork.
- **No `--agent`, no per-tier sandbox, no per-tier approval.** Only `--yolo` for AutoAll.
- **No `--add-dir`, no per-file flag, no image flag** → all three universal options shim to S-3 prompt-block.
- Native `--cwd / -c` for CWD.

## Mapping summary

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `crush run [prompt...]` | |
| `ModeInteractive` | `crush` | |
| `ModeResume` | `crush run --session <id>` | |
| `Continue` | `crush run --continue` | |
| `Fork` | unsupported | |
| `CWD` | `--cwd / -c <dir>` | |
| `Model` | `--model` | accepts `provider/model` |
| `Agent` | unsupported | |
| `OutputJSON` / `OutputStreamJSON` | unsupported | no `--format` flag |
| `Sandbox*` (read-only / workspace-write) | unsupported | |
| `SandboxDangerFullAccess` | `--yolo` | **dangerous**; opt-in required |
| `ApprovalAsk` | (default) | |
| `ApprovalPlan` / `AutoEdit` / `Never` | unsupported | refused per anti-shim |
| `ApprovalAutoAll` | `--yolo` | **dangerous**; opt-in required |
| `AddDirs` / `Files` / `Images` | S-3 (prompt-block) | |

## Recognized Config keys (`crush.*` namespace)

| Key | Native flag | Type |
|---|---|---|
| `crush.small_model` | `--small-model` | string |
| `crush.data_dir` | `-D/--data-dir <dir>` | path |
| `crush.host` | `-H/--host <unix-or-ws>` | string — connect to a running crush server |
| `crush.quiet` | `--quiet` / `-q` | bool — hide spinner |
| `crush.verbose` | `--verbose` / `-v` | bool |

## Notes

- `crush server` (long-running daemon) and `crush attach <url>` (server connect) are out of scope for invocation; use `crush.host` Config for client-side server connection.
- `crush models` / `crush projects` / `crush stats` / `crush dirs` are management commands.
- `crush login` / `crush logout` are authentication management.
