# vibe adapter

Invocation adapter for Mistral Vibe CLI (`vibe` binary).

## Last verified

- Date: 2026-05-09
- Binary: `vibe` 2.9.3
- Help artifact: `.tlc/tracks/uxp-agent-cli-facade/help/vibe.txt`

## Distinctive shape: S-4 builtin-agent shim

vibe's `--agent` flag accepts builtin names that are also approval modes:

| Vibe builtin | Universal | Behavior |
|---|---|---|
| `default` | (default) | normal prompts |
| `plan` | `ApprovalPlan` | plan-mode |
| `accept-edits` | `ApprovalAutoEdit` | auto-approve edits |
| `auto-approve` | `ApprovalAutoAll` | auto-approve everything |

This makes `Invocation.Agent` and `Invocation.Approval` mutually exclusive on vibe — both want to consume the `--agent` slot. The adapter:
- Accepts caller-set `Agent` if `Approval` is `Default` / `Ask` (no shim needed).
- Accepts caller-set `Approval` (any of plan/auto-edit/auto-all) if `Agent` is empty, mapping it to the corresponding builtin and emitting an S-4 warning diagnostic.
- **Refuses** with a hard error if both are set to non-default values.

## Other notable surface

- **No `--model` flag.** vibe selects model via config; `Invocation.Model` is refused. Use `--agent` to switch behavior.
- **No `--add-dir`, no `--file`, no `--image`** flags. AddDirs / Files / Images all go through S-3 prompt-block.
- `--output text|json|streaming` (note: `streaming`, not `stream-json` — different name).
- Native `--workdir <dir>` for CWD.
- No sandbox flag.

## Mapping summary

| Universal | Native | Notes |
|---|---|---|
| `ModeRun` | `-p/--prompt <text>` | |
| `ModeInteractive` | (default) | |
| `ModeResume` | `--resume <id>` | |
| `Continue` | `-c/--continue` | |
| `Fork` | unsupported | |
| `CWD` | `--workdir <dir>` | |
| `Model` | unsupported | use `--agent` for behavior selection |
| `Agent` | `--agent <name>` | builtins or custom from `~/.vibe/agents/` |
| `OutputJSON` | `--output json` | |
| `OutputStreamJSON` | `--output streaming` | |
| `Sandbox*` | unsupported | no sandbox flag |
| `ApprovalAsk` | (default) | |
| `ApprovalPlan` | `--agent plan` (S-4) | |
| `ApprovalAutoEdit` | `--agent accept-edits` (S-4) | |
| `ApprovalAutoAll` | `--agent auto-approve` (S-4) | **dangerous**; opt-in required |
| `ApprovalNever` | unsupported | |
| `AddDirs` / `Files` / `Images` | S-3 (prompt-block) | |

## Recognized Config keys (`vibe.*` namespace)

| Key | Native flag | Type |
|---|---|---|
| `vibe.max_turns` | `--max-turns` | string-numeric |
| `vibe.max_price` | `--max-price` | string-numeric (dollars) |
| `vibe.enabled_tools` | `--enabled-tools` (repeatable) | comma-list of tool names, glob (`bash*`), or regex (`re:edit_.*`) |
| `vibe.trust` | `--trust` | bool — trust working dir for this invocation only |

## Notes

- `vibe --setup` is a one-shot config command, not exposed via this adapter.
- `vibe agents` (the management family for custom agents) is out of scope for invocation.
