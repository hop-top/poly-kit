# codex adapter

Invocation adapter for OpenAI Codex CLI (`codex` binary).

Last verified 2026-05-09 against `codex-cli` 0.130.0 (top-level plus the
`exec`, `resume` and `fork` subcommands). Source of truth: `Mappings()` in
`mappings.go`; the parity table in `go/core/uxp/README.md` is built from it.

## Mode routing

`ModeInteractive` is bare `codex [PROMPT]`; `ModeRun` is `codex exec
[PROMPT]`; `ModeResume` is `codex exec resume [SESSION_ID|--last]
[PROMPT]`; `ModeResume` with `Fork` is `codex fork [SESSION_ID|--last]
[PROMPT]`.

`codex resume` without `exec` is the interactive TUI flavor; the adapter
prefers headless `exec resume` for `ModeResume`. For the TUI variant, use
`ModeInteractive` plus `Config["codex.profile"]` or `ExtraArgs`.

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
| `OutputJSON` | `-o/--output-last-message <FILE>` (shim) | **requires `Config["codex.output_last_message_path"]`**: codex writes the final message to a file, not stdout |
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

- **S-1 (`expandToParentDirs`)** for `Invocation.Files` → `--add-dir`, deduped against caller-provided `AddDirs`.
- **S-3 (`formatFileBlock`)** prepended to the positional prompt when `Files` is non-empty.
- **S-6 (sandbox/approval cross-shim)**, codex-only. `ApprovalPlan` has no native flag, so the adapter combines `-s read-only` (no writes possible) with `-a never` (no prompts) as the closest peer. An explicitly supplied sandbox tier is preserved and only `-a never` is added. A diagnostic records the cross-shim.

## Anti-shims (refused mappings)

| Refused | Why |
|---|---|
| `ApprovalAutoEdit` | no native auto-edit; `--dangerously-bypass-approvals-and-sandbox` would change authority semantics. Diagnostic names `ApprovalAsk` and `ApprovalNever` |
| `ApprovalAutoAll`, `SandboxDangerFullAccess` | need `Config["uxp.allow_dangerous"]="true"` |
| `OutputJSON` without `codex.output_last_message_path` | the final-message JSON is file-based; refusing beats picking a default path |
| `OutputJSON` / `OutputStreamJSON` with `ModeInteractive` | both require the `exec` subcommand |
| `Fork = true` outside `ModeResume` | fork is a resume modifier |
| `ModeResume` without `SessionID` and without `Continue = true` | nothing to resume |
| `Agent != ""` | use the `codex.profile` key for `--profile` configuration profiles |

## Recognized Config keys

The `codex.*` namespace covers `profile`, `config`, `enable`, `disable`,
`search`, `skip_git_repo_check`, `ephemeral`, `ignore_user_config`,
`ignore_rules`, `output_schema`, `output_last_message_path`, `oss` and
`local_provider`. Flags, types and defaults are tabled in the
[UXP reference](../../../../../../docs/adopters/reference/uxp.md#codex-codex).

## Notes

- `codex apply` (apply the latest agent diff as `git apply`) is not exposed here; it is a host-side tool, not an invocation pattern. `codex review`, `codex cloud`, `codex remote-control` and `codex app-server` are likewise out of scope, being distinct CLI surfaces rather than flag-level shims.
- `--no-alt-screen` (TUI scrollback compat) and `--remote` (websocket connect) are TUI-only; pass them via `ExtraArgs`.

## See also

- [`go/core/uxp/README.md`](../../../README.md): parity matrices, shim catalog, universal `Config` keys
- [UXP invoke reference](../../../../../../docs/adopters/reference/uxp.md)
