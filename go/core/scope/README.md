# scope

Path policy guardrails for kit primitives that touch the filesystem.

## When to use

| Situation | API |
|-----------|-----|
| Build a policy in code | `scope.New().Allow(...).Deny(...)` |
| Use the package-level default policy | `scope.Default()` |
| Decide whether (path, op) is allowed | `Policy.Check(path, op)` |
| Enforce policy (errors in Strict, logs in Warn) | `Policy.Enforce(path, op)` |
| Load a declarative policy from disk | `scope.FromConfig("mytool")` |
| Get default deny patterns | `scope.SecretPaths()` |
| Build a pattern for a tool's XDG dir | `scope.ToolConfig("mytool")` (or Data/Cache/State/Runtime/Bin) |
| Build a pattern for a user dir | `scope.UserDocs()` (or Downloads/Desktop/Pictures/Music/Videos/Home) |
| Get system dirs (`/etc`, `/usr`, …) | `scope.SystemDirs()` |
| Swap default policy in a test | `restore := scope.SetDefault(p); t.Cleanup(restore)` |
| Deep-copy a policy | `clone := orig.Snapshot()` |

## Modes

- **Strict** (default): Denied → returns `ErrDenied`; Unknown (no rule
  matched) is also Denied.
- **Warn**: Denied → logs at WARN, returns nil; Unknown is allowed.
- **Prompt**: Denied → invokes the registered `PromptFunc`; if it returns
  true the call proceeds; missing callback is treated as deny.

## Decision algorithm

Deny-wins:

1. If any matching deny rule matches `(path, op)` → **Denied**.
2. Else if any matching allow rule matches → **Allowed**.
3. Otherwise → **Unknown** (treated as Denied in Strict, Allowed elsewhere).

Symlinks are resolved at `Check` time. For nonexistent paths the resolver
walks up to the deepest existing ancestor and re-attaches the missing tail
so deny rules still match by intent (e.g. denying writes to `~/.ssh/new_key`
even before the file exists).

Patterns use [doublestar v4](https://github.com/bmatcuk/doublestar) syntax.
A leading `~` expands to the user home; on Windows, `%APPDATA%`,
`%LOCALAPPDATA%`, and `%USERPROFILE%` macros expand to their env values
(falling back to the macro if unset, which simply never matches).

## Default deny list

`SecretPaths()` returns the platform-tailored deny patterns shipped in
[`scope-defaults.json`](scope-defaults.json). The file is the canonical,
polyglot source of truth (Go embeds a copy; TS + Python ports load from
[`contracts/parity/scope-defaults.json`](../../../contracts/parity/scope-defaults.json)).

The list covers SSH keys, cloud credentials (`~/.aws`, `~/.azure`,
`~/.config/gcloud`), GPG / PGP / TLS material, kube configs, browser
cookie + login stores (Chrome, Arc, Brave, Edge, Firefox, Safari),
macOS keychains, and the corresponding Windows DPAPI vaults / PuTTY
profiles.

`init()` in `defaults.go` pre-populates `scope.Default()` with this set
denied, so any binary that links the scope package gets a hardened
singleton out of the box.

## Declarative config

Tools (and ops) can declare policy in YAML — load via `scope.FromConfig(tool)`:

```yaml
# ~/.config/<tool>/scope.yaml
mode: strict
allow:
  - "~/Documents/**"
  - tool:data       # macro -> ToolData(<tool>)
  - tool:cache
deny:
  - "~/Documents/Private/**"
```

Macros: `tool:config`, `tool:data`, `tool:cache`, `tool:state`,
`tool:runtime`, `tool:bin`. System config (`/etc/xdg/<tool>/scope.yaml`) is
read first, then the user config is merged on top. Per-user `mode` wins;
rules from both files are appended (deny-wins still applies at check time).

## CLI introspection

`kit scope show [--tool <name>]` — print the effective policy.
`kit scope check <path> [--op read|write|exec] [--tool <name>]` — single check.
`kit scope test <path>... [--op ...] [--tool <name>]` — bulk check.

Exit codes: `0` allowed, `1` denied (or any deny in a bulk run), `2` usage
error. All commands honour `--format table|json|yaml`.

## Test isolation

```go
func TestThing(t *testing.T) {
    restore := scope.SetDefault(scope.New().Allow(scope.UserDocs()...))
    t.Cleanup(restore)
    // ... test code that goes through scope.Default() ...
}
```

`SetDefault` returns a restore func that puts the previous singleton back.
Always pair with `t.Cleanup` so the swap doesn't bleed into sibling tests.
Nested `SetDefault` calls unwind in LIFO order.

## How to disable for trusted tools

Disabling guardrails is an anti-pattern: the protection exists because
agents acting on behalf of users routinely encounter paths that look safe
but aren't. That said, kit ships two escape hatches for the rare tool that
genuinely needs unfettered FS access:

```go
// 1. Per-tool: replace Default with a permissive policy.
scope.SetDefault(scope.New().SetMode(scope.Warn))

// 2. Global xdg bypass: disable the xdg → scope guard hook.
xdg.SetGuard(nil)
```

Both should be paired with a comment explaining why and a tracking
ticket to either remove the bypass or shrink it to a specific allow list.
