# claude adapter

Invocation adapter for Claude Code (`claude` binary).

## Last verified

- Date: 2026-05-09
- Binary: `claude` 2.1.118 (Claude Code)
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/claude.txt`

Re-run `claude --help > .tlc/tracks/uxp-agent-cli-facade/help/claude.txt` and update this section if the surface changes; if the diff touches a flag below, update the matrix in `go/core/uxp/README.md` (auto-generated from `Mappings()`).

## Mapping summary

Source of truth: `Mappings()` in `mappings.go`. The package-level
`go/core/uxp/README.md` parity table is built from that slice.

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `-p` | print mode |
| `ModeInteractive` | (default) | bare `claude [prompt]` |
| `ModeResume` | `--resume <id>` | + `-p` for headless resume |
| `Continue` | `--continue` / `-c` | resume latest |
| `Fork` | `--fork-session` | only with `--resume` or `--continue` |
| `CWD` | `CommandSpec.Dir` | claude has no `--cd` flag |
| `Model` | `--model` | accepts alias (`sonnet`) or full name |
| `Agent` | `--agent` | external agent name; or `--agents <json>` for inline (via Config) |
| `OutputJSON` | `--output-format json` | single result; requires `-p` |
| `OutputStreamJSON` | `--output-format stream-json` | streaming; requires `-p` |
| `SandboxReadOnly` | `--permission-mode plan` | shim — claude has no first-class sandbox tier |
| `SandboxWorkspaceWrite` | (default) | shim — implicit via tool policy |
| `SandboxDangerFullAccess` | `--dangerously-skip-permissions` | **dangerous**; requires opt-in |
| `ApprovalPlan` | `--permission-mode plan` | |
| `ApprovalAutoEdit` | `--permission-mode acceptEdits` | |
| `ApprovalAutoAll` | `--permission-mode bypassPermissions` | **dangerous**; requires opt-in |
| `ApprovalNever` | `--permission-mode dontAsk` | shim — closest peer |
| `AddDirs` | `--add-dir <dirs...>` | variadic |
| `Files` | S-1 + S-3 | claude `--file` is for downloaded resources, not local files |
| `Images` | S-3 (prompt-block) | no headless image flag; TUI-only via stdin/clipboard |

## Shims invoked

- **S-1 (`expandToParentDirs`)** — applied to `Invocation.Files`. Reduces files to their minimal containing directories; passes those via `--add-dir`. Diagnostic: warning, scope-widening note.
- **S-3 (`formatFileBlock`)** — prepended to `Invocation.Prompt` whenever `Files` or `Images` is non-empty. Diagnostic: warning per slice.

## Anti-shims (refused mappings)

- `Approval = ApprovalAutoAll` without `Config["uxp.allow_dangerous"]="true"` → **error**. Native flag is dangerous; caller must opt in explicitly.
- `Sandbox = SandboxDangerFullAccess` without opt-in → **error**, same reason.
- `Output = OutputJSON` or `OutputStreamJSON` with `Mode = ModeInteractive` → **error**. Both flags require `-p`.
- `Fork = true` outside `Mode = ModeResume` → **error**. claude requires `--fork-session` to pair with `--resume`/`--continue`.
- `Mode = ModeResume` without `SessionID` and without `Continue = true` → **error**. Ambiguous request.

## Recognized Config keys (`claude.*` namespace)

| Key | Native flag | Type |
|---|---|---|
| `claude.system_prompt` | `--system-prompt` | string |
| `claude.append_system_prompt` | `--append-system-prompt` | string |
| `claude.allowed_tools` | `--allowedTools` (repeatable) | comma-list (use `\,` to escape commas in values) |
| `claude.disallowed_tools` | `--disallowedTools` (repeatable) | comma-list |
| `claude.tools` | `--tools` | string (e.g. `"Bash,Edit,Read"` or `"default"`) |
| `claude.settings` | `--settings` | path or JSON string |
| `claude.max_budget_usd` | `--max-budget-usd` | string-numeric |
| `claude.session_id` | `--session-id` | UUID |
| `claude.fallback_model` | `--fallback-model` | model name |
| `claude.effort` | `--effort` | one of `low|medium|high|xhigh|max` |

Unknown `claude.*` keys emit an `info` diagnostic. Keys outside the `claude.*` namespace (other than the universal `uxp.*` keys) are silently ignored — that's by design so a single `Config` map can carry settings for multiple adapters.

## Notes

- `--file file_id:relative_path` is for files claude itself downloaded to its session; not a local-file attach. The adapter does not expose this surface — use `Files` (which shims to S-1+S-3) for local files.
- Tool policy (`--allowedTools`, `--disallowedTools`, `--tools`) is exposed only via Config keys, not as a universal option, because no other adapter has the same shape.
- `--dangerously-skip-permissions` and `--allow-dangerously-skip-permissions` are distinct: the former actually bypasses; the latter is a *capability* flag enabling the bypass option in the session. The adapter currently emits only the former on `SandboxDangerFullAccess`. If a caller needs the capability flag (e.g. to be present at startup but not auto-applied), pass it via `ExtraArgs`.
