# codex adapter

Invocation adapter for OpenAI Codex CLI (`codex` binary).

## Last verified

- Date: 2026-05-09
- Binary: `codex-cli` 0.130.0
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/codex.txt`
  (top-level + `exec`, `resume`, `fork` subcommands)

## Mode → subcommand routing

Codex has multiple subcommands; this adapter routes:

| Mode | Subcommand |
|---|---|
| `ModeInteractive` | (none — bare `codex [PROMPT]`) |
| `ModeRun` | `codex exec [PROMPT]` |
| `ModeResume` | `codex exec resume [SESSION_ID|--last] [PROMPT]` |
| `ModeResume + Fork` | `codex fork [SESSION_ID|--last] [PROMPT]` |

`codex resume` (without `exec`) is the interactive TUI flavor; the
adapter prefers headless `exec resume` for `ModeResume`. If a caller
needs the TUI variant for resume, they should use `ModeInteractive`
plus `Config["codex.profile"]` or `ExtraArgs`.

## Mapping summary

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `exec` | |
| `ModeResume` | `exec resume <id>` | headless |
| `Continue` | `exec resume --last` | |
| `Fork` | `fork <id>` / `fork --last` | only CLI besides claude/opencode/goose with native fork |
| `CWD` | `-C/--cd <DIR>` | adapter also sets `CommandSpec.Dir` |
| `Model` | `-m/--model` | |
| `Agent` | unsupported | use `codex.profile` for `--profile` |
| `OutputJSON` | `-o/--output-last-message <FILE>` (shim) | **requires `Config["codex.output_last_message_path"]`** — codex writes the final message to a file, not stdout |
| `OutputStreamJSON` | `--json` | JSONL events to stdout |
| `SandboxReadOnly` | `-s read-only` | full tier parity |
| `SandboxWorkspaceWrite` | `-s workspace-write` | |
| `SandboxDangerFullAccess` | `-s danger-full-access` | **dangerous**; opt-in required |
| `ApprovalAsk` | `-a on-request` | |
| `ApprovalPlan` | **S-6** (`-s read-only -a never`) | no native plan mode |
| `ApprovalAutoEdit` | unsupported | refused per anti-shim |
| `ApprovalAutoAll` | `--dangerously-bypass-approvals-and-sandbox` | **dangerous**; opt-in required |
| `ApprovalNever` | `-a never` | |
| `AddDirs` | `--add-dir <DIR>` (repeatable) | |
| `Files` | S-1 + S-3 | parent-dir reduce → `--add-dir`; prompt-block listing |
| `Images` | `-i/--image <FILE>...` | variadic, native |

## Shims invoked

- **S-1 (`expandToParentDirs`)** for `Invocation.Files` → `--add-dir`. Dedups against caller-provided `AddDirs`.
- **S-3 (`formatFileBlock`)** prepended to the positional prompt when `Files` is non-empty.
- **S-6 (sandbox/approval cross-shim)** — codex-only. `ApprovalPlan` lacks a native flag; the adapter combines `-s read-only` (no writes possible) with `-a never` (no prompts) as the closest peer to plan mode. If the caller already supplied a sandbox tier explicitly, S-6 preserves that tier and only adds `-a never`. Diagnostic emitted to record the cross-shim.

## Anti-shims (refused mappings)

- `Approval = ApprovalAutoEdit` → **error**. Codex has no native auto-edit mode and `--dangerously-bypass-approvals-and-sandbox` would change authority semantics. The diagnostic names the safer alternatives (`ApprovalAsk`, `ApprovalNever`).
- `Approval = ApprovalAutoAll` without `Config["uxp.allow_dangerous"]="true"` → **error**.
- `Sandbox = SandboxDangerFullAccess` without opt-in → **error**.
- `Output = OutputJSON` without `Config["codex.output_last_message_path"]` → **error**. Codex's final-message JSON is file-based; refusing rather than picking a default path keeps the contract explicit.
- `Output = OutputJSON` or `OutputStreamJSON` with `Mode = ModeInteractive` → **error**. Both require the `exec` subcommand.
- `Fork = true` outside `Mode = ModeResume` → **error**.
- `Mode = ModeResume` without `SessionID` and without `Continue = true` → **error**.
- `Agent != ""` → **error**. Use `codex.profile` Config key for `--profile`-style configuration profiles.

## Recognized Config keys (`codex.*` namespace)

| Key | Native flag | Type |
|---|---|---|
| `codex.profile` | `-p/--profile` | string (config profile name from `~/.codex/config.toml`) |
| `codex.config` | `-c <key=value>` (repeatable) | comma-list of `key=value` pairs (TOML-parsed values) |
| `codex.enable` | `--enable <feature>` (repeatable) | comma-list |
| `codex.disable` | `--disable <feature>` (repeatable) | comma-list |
| `codex.search` | `--search` | bool |
| `codex.skip_git_repo_check` | `--skip-git-repo-check` | bool |
| `codex.ephemeral` | `--ephemeral` | bool — disables session persistence |
| `codex.ignore_user_config` | `--ignore-user-config` | bool |
| `codex.ignore_rules` | `--ignore-rules` | bool |
| `codex.output_schema` | `--output-schema <FILE>` | path to JSON Schema |
| `codex.output_last_message_path` | `-o/--output-last-message <FILE>` | path; **required for `OutputJSON`** |
| `codex.oss` | `--oss` | bool — open-source provider |
| `codex.local_provider` | `--local-provider` | string (`lmstudio` or `ollama`) |

## Notes

- `codex apply` (top-level subcommand to apply the latest agent diff as `git apply`) is not exposed via this adapter; it's a host-side tool, not an invocation pattern.
- `codex review`, `codex cloud`, `codex remote-control`, `codex app-server`, etc. are also not in scope — they are distinct CLI surfaces, not flag-level shims.
- `--no-alt-screen` (TUI scrollback compat) and `--remote` (websocket connect) are TUI-only; pass via `ExtraArgs` if needed.
