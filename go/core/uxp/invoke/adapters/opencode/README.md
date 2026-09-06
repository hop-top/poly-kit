# opencode adapter

Invocation adapter for opencode CLI. Last verified 2026-05-09 against
`opencode` 1.14.30 (top-level plus the `run` subcommand). Source of truth:
`Mappings()` in `mappings.go`; the parity tables in the UXP reference are
built from it.

## Distinctive shape

opencode is the only adapter where the universal **AddDirs** has no native
flag but **Files** does. The S-2 shim (`enumerateDirFiles`) inverts the
usual direction: `AddDirs` walks each directory and emits one `--file`
argument per file. This is the case the spec calls out as "takes --files
but not dir, list files to add".

## Mode routing

`ModeInteractive` is bare `opencode [project]`; `ModeRun` is `opencode run
[message..]`; `ModeResume` is `opencode run --session <id>` or
`--continue`; `Fork` is a `--fork` modifier on the resume. `opencode
resume` is not a separate subcommand: resume is a flag on `opencode run`.

## Mapping summary

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `run [message..]` | |
| `ModeResume` | `run --session <id>` | |
| `Continue` | `run --continue` | |
| `Fork` | `--fork` | pairs with `--continue` or `--session` |
| `CWD` | `--dir <dir>` | run/resume; bare TUI uses positional |
| `Model` | `-m/--model` | accepts `provider/model` |
| `Agent` | `--agent <name>` | |
| `OutputJSON` | `--format json` (shim) | opencode emits JSONL event stream; caller must reduce to final assistant message |
| `OutputStreamJSON` | `--format json` | native event stream |
| `SandboxReadOnly`, `SandboxWorkspaceWrite` | unsupported | no per-tier sandbox |
| `SandboxDangerFullAccess`, `ApprovalAutoAll` | `--dangerously-skip-permissions` | **dangerous**; opt-in required |
| `ApprovalAsk` | (default) | |
| `ApprovalPlan`, `ApprovalNever` | unsupported | |
| `ApprovalAutoEdit` | unsupported | refused per anti-shim |
| `AddDirs` | **S-2** (enumerate → `--file`) | shim, opencode has no `--add-dir` |
| `Files` | `-f/--file <path>` (repeatable) | |
| `Images` | `-f/--file <path>` (shim) | no distinct image attachment surface |

## Shims invoked

- **S-2 (`enumerateDirFiles`)**: for each `Invocation.AddDirs` entry, walks the directory and emits `--file <path>` per regular file. Honors `Config["uxp.shim.dir_to_files_max"]` (default 200). Files already in `Invocation.Files` are filtered out to avoid double-listing. Overflow is a **hard error** with a diagnostic naming the offending dir and cap.
- **S-3 not used**: opencode accepts files natively, so the prompt-block fallback is unnecessary and the composed prompt is just `Invocation.Prompt`.

## Anti-shims (refused mappings)

| Refused | Why |
|---|---|
| `SandboxReadOnly`, `SandboxWorkspaceWrite` | no per-tier sandbox; configure via `opencode config` |
| `SandboxDangerFullAccess`, `ApprovalAutoAll` | need `Config["uxp.allow_dangerous"]="true"` |
| `ApprovalAutoEdit` | no native auto-edit; refuses to degrade to the dangerous bypass |
| `ApprovalPlan`, `ApprovalNever` | no native equivalent |
| `Fork = true` outside `ModeResume` | fork is a resume modifier |
| `ModeResume` without `SessionID` and without `Continue = true` | nothing to resume |

## Recognized Config keys

The `opencode.*` namespace covers `variant`, `thinking`, `share`, `title`,
`command`, `attach`, `password`, `port` and `pure`. Flags, types and
defaults are tabled in the
[UXP reference](../../../../../../docs/adopters/reference/uxp.md#opencode-opencode).
Universal `Config["uxp.shim.dir_to_files_max"]` controls the S-2 cap
(default 200).

## Notes

- The `provider/model` spec (for example `anthropic/sonnet-4-6`) is honored as-is; `Model` carries the full string.
- Web search is plugin-only, so the `tools.go` entry marks it `MappingShim`: support depends on which plugins and MCP servers the user configured.
- The `task` subcommand spawns subagents but is not a flag-level surface. It is an in-conversation tool, recorded in `ToolCapabilities()` and not in `Mappings()`.
- No JSONL to final-message reducer ships for `OutputJSON`; callers consume the diagnostic and write their own.

## See also

- [`go/core/uxp/README.md`](../../../README.md): package README
- [UXP reference](../../../../../../docs/adopters/reference/uxp.md): parity matrices, shim catalog, per-adapter `Config` keys
