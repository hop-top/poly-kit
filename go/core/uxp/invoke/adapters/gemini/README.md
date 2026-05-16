# gemini adapter

Invocation adapter for Google Gemini CLI (`gemini` binary).

## Last verified

- Date: 2026-05-09
- Binary: `gemini` 0.40.1
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/gemini.txt`

## Mapping summary

Source of truth: `Mappings()` in `mappings.go`.

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `-p <prompt>` | non-interactive |
| `ModeInteractive` | (default) | bare `gemini [query]` |
| `ModeResume` | `--resume <id>` | id can be UUID or integer index |
| `Continue` | `--resume latest` | gemini has no `--continue` flag |
| `Fork` | unsupported | refused with error |
| `CWD` | `CommandSpec.Dir` | no `--cd` flag |
| `Model` | `-m/--model` | |
| `Agent` | unsupported | no `--agent` flag |
| `OutputJSON` | `--output-format json` | full parity with claude |
| `OutputStreamJSON` | `--output-format stream-json` | |
| `SandboxReadOnly` | `--approval-mode plan` | shim |
| `SandboxWorkspaceWrite` | `--sandbox` (boolean) | shim — no tier value |
| `SandboxDangerFullAccess` | `--yolo` | **dangerous**; opt-in required |
| `ApprovalPlan` | `--approval-mode plan` | |
| `ApprovalAutoEdit` | `--approval-mode auto_edit` | |
| `ApprovalAutoAll` | `--approval-mode yolo` | **dangerous**; opt-in required |
| `ApprovalNever` | unsupported | no equivalent to claude's `dontAsk` |
| `AddDirs` | `--include-directories <dir>` | repeatable; one flag per dir |
| `Files` | S-1 + S-3 | parent-dir reduce → `--include-directories`; prompt-block listing |
| `Images` | S-3 (prompt-block) | no headless image flag |

## Shims invoked

- **S-1 (`expandToParentDirs`)** for `Invocation.Files` → `--include-directories`. Dedups against `Invocation.AddDirs` to avoid double-listing.
- **S-3 (`formatFileBlock`)** prepended to the `-p` prompt value when `Files` or `Images` non-empty.

## Anti-shims (refused mappings)

- `Fork = true` → **error** (no native fork; resume + fresh session would lose lineage).
- `Agent != ""` → **error** (no `--agent` flag).
- `Approval = ApprovalNever` → **error** (no equivalent; refuse rather than shim ambiguously to `auto_edit`).
- `Approval = ApprovalAutoAll` without `Config["uxp.allow_dangerous"]="true"` → **error**.
- `Sandbox = SandboxDangerFullAccess` without opt-in → **error**.

## Recognized Config keys (`gemini.*` namespace)

| Key | Native flag | Type |
|---|---|---|
| `gemini.policy` | `--policy` (repeatable) | comma-list of policy file paths |
| `gemini.admin_policy` | `--admin-policy` (repeatable) | comma-list |
| `gemini.allowed_mcp_server_names` | `--allowed-mcp-server-names` (repeatable) | comma-list |
| `gemini.extensions` | `--extensions` / `-e` (repeatable) | comma-list of extension names |
| `gemini.skip_trust` | `--skip-trust` | bool (true/1/yes/on) |
| `gemini.raw_output` | `--raw-output --accept-raw-output-risk` | bool — pairs both flags |
| `gemini.screen_reader` | `--screen-reader` | bool |

`--allowed-tools` is documented as DEPRECATED in favor of `--policy` — adapter intentionally omits it. Pass via `ExtraArgs` if a caller needs the legacy surface.

## Notes

- Resume value can be UUID, integer index (`--resume 5`), or literal `"latest"`. Adapter only emits whatever the caller passes via `SessionID` (or `latest` for `Continue=true`).
- `--prompt-interactive`/`-i` (run prompt then drop into interactive) is not part of the universal `Mode`; pass via `ExtraArgs` if needed.
- `--worktree` for git-worktree-isolated sessions is gemini-specific; pass via `ExtraArgs` or future `gemini.worktree` Config key when the use-case appears.
