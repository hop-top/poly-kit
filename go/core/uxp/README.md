# uxp

User experience primitives and project context discovery for agent
CLIs.

## Concerns

`uxp` (the parent package) covers **identity + discovery**:
- 15 known agent CLIs (claude, gemini, codex, opencode, copilot,
  cursor-agent, qwen, kimi, vibe, goose, crush, plus
  detection-only entries: amp, antigravity, tabnine, windsurf).
- `Adapter` interface for `Detect()` / `Capabilities()`.
- `StorePaths` / `ProjectKeyStrategy` for cross-CLI session-store
  lookup.

`uxp/invoke` (sub-package) covers **invocation**:
- `Build(Invocation)` translates a normalized request into native
  argv for the 11 in-scope CLIs.
- Per-option `MappingSupport` (native / shim / unsupported /
  dangerous) so callers can explain degradation before running.
- `ToolCapability` taxonomy normalizing built-in agent tools
  (Bash → `shell.exec`, Read → `file.read`, etc.) across vendors.
- Anti-shim guards: `ApprovalAutoEdit` never silently degrades to
  a target's auto-all flag; `Fork` never emulates via "resume +
  fresh session"; `Sandbox*` never cross-shims to container
  isolation.

## Universal-option parity

The matrix below is auto-generated from each adapter's
`Mappings()` slice via `go/core/uxp/internal/parityreadme/main.go`.
Run `go generate ./go/core/uxp/...` to regenerate; CI fails on a
non-empty diff.

<!-- parity:start -->
| Universal | claude | gemini | codex | opencode | copilot | cursor-agent | qwen | kimi | vibe | goose | crush |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `ModeRun` | N | N | N | N | N | N | N | N | N | N | N |
| `ModeInteractive` | N | N | N | N | N | N | N | N | N | N | N |
| `ModeResume` | N | N | N | N | N | N | N | N | N | N | N |
| `Continue` | N | N | N | N | N | N | N | N | N | N | N |
| `Fork` | N | U | N | N | U | U | U | U | U | N | U |
| `CWD` | N | N | N | N | N | N | N | N | N | N | N |
| `Model` | N | N | N | N | N | N | N | N | U | N | N |
| `Agent` | N | U | U | N | N | U | U | N | N | S | U |
| `OutputText` | N | N | N | N | N | N | N | N | N | N | N |
| `OutputJSON` | N | N | S | S | S | N | N | S | N | N | U |
| `OutputStreamJSON` | N | N | N | N | N | N | N | N | N | N | U |
| `SandboxReadOnly` | S | S | N | U | U | U | S | U | U | U | U |
| `SandboxWorkspaceWrite` | S | S | N | U | U | U | S | U | U | U | U |
| `SandboxDangerFullAccess` | D | D | D | D | D | D | D | U | U | U | D |
| `ApprovalAsk` | N | N | N | N | N | N | N | N | N | N | N |
| `ApprovalPlan` | N | N | S | U | U | U | N | N | S | U | U |
| `ApprovalAutoEdit` | N | N | U | U | U | U | N | U | S | U | U |
| `ApprovalAutoAll` | D | D | D | D | D | D | D | D | D | U | D |
| `ApprovalNever` | S | U | N | U | U | U | U | U | U | U | U |
| `AddDirs` | N | N | N | S | N | S | N | N | S | S | S |
| `Files` | S | S | S | N | S | S | S | S | S | S | S |
| `Images` | S | S | N | S | S | S | S | S | S | S | S |

Legend: `N` native · `S` shim · `U` unsupported · `D` dangerous (opt-in required).
<!-- parity:end -->

## Tool capability parity

<!-- tools:start -->
| Tool | claude | gemini | codex | opencode | copilot | cursor-agent | qwen | kimi | vibe | goose | crush |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `shell.exec` | N | N | N | N | N | N | N | N | N | N | N |
| `file.read` | N | N | S | N | N | N | N | N | N | N | N |
| `file.write` | N | N | S | N | N | N | N | N | N | N | N |
| `file.edit` | N | N | N | N | N | N | N | N | N | N | N |
| `file.search` | N | N | S | N | N | S | N | S | S | S | S |
| `web.search` | N | N | N | S | S | U | S | S | U | S | U |
| `web.fetch` | N | N | S | N | N | U | S | S | U | S | U |
| `todo.write` | N | S | U | N | U | U | U | U | U | U | U |
| `task.spawn` | N | S | U | N | U | U | U | U | U | U | U |
| `plan.update` | U | U | N | U | U | U | U | U | S | U | U |
| `mcp.call` | N | N | N | N | N | N | N | N | U | N | S |
| `image.read` | S | N | N | S | U | U | S | U | U | U | U |
| `browser.operate` | S | U | U | U | U | U | U | U | U | U | U |
| `user.message` | N | U | U | U | N | U | U | N | U | U | U |

Legend: `N` native · `S` shim · `U` unsupported.
<!-- tools:end -->

## Adapters

- [`invoke/adapters/claude/`](invoke/adapters/claude/)
- [`invoke/adapters/gemini/`](invoke/adapters/gemini/)
- [`invoke/adapters/codex/`](invoke/adapters/codex/)
- [`invoke/adapters/opencode/`](invoke/adapters/opencode/)
- [`invoke/adapters/copilot/`](invoke/adapters/copilot/)
- [`invoke/adapters/cursoragent/`](invoke/adapters/cursoragent/)
- [`invoke/adapters/qwen/`](invoke/adapters/qwen/)
- [`invoke/adapters/kimi/`](invoke/adapters/kimi/)
- [`invoke/adapters/vibe/`](invoke/adapters/vibe/)
- [`invoke/adapters/goose/`](invoke/adapters/goose/)
- [`invoke/adapters/crush/`](invoke/adapters/crush/)

Each adapter README documents the per-CLI flag mapping, shim
inventory, anti-shim refusals, and recognized `Config` keys.

## Detection-only CLIs

These are registered for store-path discovery but have no
invocation adapter (different shape, or not an agent CLI):

- **amp** — thread-subcommand shape; separate track.
- **antigravity** (`agy`) — VS Code-fork editor binary.
- **tabnine** — IDE plugin / completion only.
- **windsurf** — editor family.

Calling `uxp/invoke.Build(Invocation{CLI: amp, …})` returns a hard
error directing the caller to the appropriate path.

## Shims

Six closed-set shims live in `invoke/shim/`. Adapters do not invent
new shims; the catalog is fixed in spec §15.5.

| Shim | Helper | Used by |
|---|---|---|
| S-1 (parent-dir reduce) | `ExpandToParentDirs` | gemini, codex, copilot, qwen, kimi |
| S-2 (enumerate dir → files) | `EnumerateDirFiles` | opencode |
| S-3 (prompt-block) | `FormatFileBlock` | every adapter without native scoping |
| S-4 (builtin-agent → approval) | inline | vibe |
| S-5 (recipe ↔ agent) | inline | goose |
| S-6 (sandbox/approval cross-shim) | inline | codex |

## Universal `Config` keys

Per-adapter Config keys use the `<cli>.<key>` namespace. Two
cross-adapter keys live under `uxp.`:

| Key | Type | Effect |
|---|---|---|
| `uxp.allow_dangerous` | bool | Required to enable any `MappingDangerous` mapping (e.g. `--yolo`, `--dangerously-skip-permissions`). |
| `uxp.shim.dir_to_files_max` | int | S-2 enumeration cap. Default 200; overflow is a hard error. |
