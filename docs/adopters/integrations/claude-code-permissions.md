# Configure Claude Code permissions via the kit-toolspec contract

Status: published
Audience: harness authors and Claude Code users adopting kit-powered
CLIs (`tlc`, `ctxt`, `wsm`, …).

## What this fixes

Today, Claude Code users hand-author every kit-powered CLI's
permissions in `~/.claude/settings.json`:

```jsonc
{
  "permissions": {
    "allow": [
      "tlc task list", "tlc task show", "tlc task list --status",
      "tlc track show", "tlc flow list",
      "ctxt search", "ctxt search --pack", "ctxt show",
      "wsm space list", "wsm context show", "wsm profile list",
      "kit symlink", "kit config path"
    ],
    "deny": [
      "tlc task delete", "tlc track delete",
      "ctxt forget", "wsm space delete"
    ]
  }
}
```

Drift-prone, hand-authored, blind to the manifest's risk metadata.
Every new subcommand needs another allowlist edit. New kit-powered
CLIs can't inherit policy.

## What the contract gives you

Kit defines a manifest consumption contract. A harness:

1. Discovers a kit-powered CLI's manifest via
   `<tool> spec --format kit-manifest`.
2. Reads the per-leaf `side_effect` (and, once commands carry it, the
   `network` axis) from each manifest entry.
3. Resolves the (`side_effect`, `network`) tuple through kit's
   default policy table (or a custom overlay) into one of
   `auto-allow`, `prompt`, or `deny`.
4. Renders the harness's native permission shape from that
   decision.

Result: one rule covers every kit-powered CLI. New tools inherit
policy for free.

## Five-line policy in Claude Code shape

The kit default already says: auto-allow read commands, auto-allow
local writes, prompt destructive, deny destructive+egress. Translated
into the Claude Code `settings.json` shape an adapter would generate:

```jsonc
// Generated from `kit toolspec policy` — DO NOT hand-edit.
{
  "permissions": {
    "rules": [
      // tier ≤ write at network=none → auto-allow.
      { "match": { "kit/side-effect": "read" }, "action": "auto-allow" },
      { "match": { "kit/side-effect": "write", "kit/network": "none" }, "action": "auto-allow" },
      // destructive prompts; destructive+egress denies.
      { "match": { "kit/side-effect": "destructive", "kit/network": "egress" }, "action": "deny" },
      { "match": { "kit/side-effect": "destructive" }, "action": "prompt" },
      // catch-all: anything not mapped prompts (fail-safe).
      { "match": {}, "action": "prompt" }
    ]
  }
}
```

Five rules. Covers `tlc`, `ctxt`, `wsm`, and every future kit-powered
CLI.

## How a harness consumes the contract

Pseudocode for the `tools/permission` resolver:

```go
import (
    "hop.top/kit/go/ai/toolspec/adapters"
    "hop.top/kit/go/ai/toolspec/policy"
)

// Once at startup: load the manifest by invoking the binary.
manifest := exec.Command("tlc", "manifest").Output()  // → toolspec.Manifest JSON
table    := policy.Default()                          // or policy.LoadOrDefault(customPath)

// Per tool call:
env := adapters.EnforceMCPRequest(manifest, []string{"tlc", "task", "delete"}, table)
switch env.Decision.Action {
case policy.ActionAutoAllow:
    // proceed silently
case policy.ActionPrompt:
    askUser(env.Decision.Reason)
case policy.ActionDeny:
    return env.Error  // JSON-RPC error envelope, code -32099
}
```

That is the whole integration. Every new kit-powered CLI plugs in
without code changes; the harness configures policy per-tier, not
per-tool.

## Customising the table

Some teams want stricter rules — for example, prompt every read in
production environments. Ship a YAML overlay:

```yaml
# production-policy.yaml
schema_version: "1.0"
rules:
  - side_effect: read
    network: none
    action: prompt
    reason: "production environment: confirm every read"
```

Pass it to your MCP host or invoke the inspector to verify the
merged table:

```sh
$ kit toolspec policy --file production-policy.yaml | jq '.rules[] | select(.side_effect=="read")'
{
  "side_effect": "read",
  "network": "none",
  "action": "prompt",
  "reason": "production environment: confirm every read",
  "source": "production-policy.yaml"
}
{
  "side_effect": "read",
  "network": "local-only",
  "action": "auto-allow",
  "reason": "local-only read on user's machine; no escape hatch",
  "source": "default.yaml"
}
```

Overlay rules win on collision; default rules fill the gaps.

## Capability negotiation for harness implementers

Two manifest schema versions exist:

| `schema_version` | What it carries |
|------------------|-----------------|
| `"1.0"` | The original layout. |
| `"1.1"` | Adds per-command fields that surface kit's command annotations. Additive: a harness that ignores unknown fields reads a 1.1 manifest as it reads 1.0. |

`kit toolspec` emits `"1.1"`. A kit-powered CLI's `<tool> spec`
emits at least the version its author passed to
`cli.RegisterSpecCommand` (the *declared* version); the manifest it
builds has the same fields whatever version it resolves to. Always
read `schema_version` from the payload (or `<tool> spec --version`)
rather than assume it.

Harnesses signal their max-supported schema version via the env var
`KIT_TOOLSPEC_SCHEMA`:

```sh
KIT_TOOLSPEC_SCHEMA=1.1 kit toolspec
KIT_TOOLSPEC_SCHEMA=1.1 mytool spec --version
```

`kit toolspec` and `<tool> spec` apply the same rules
(`toolspec.NegotiateSchemaVersion`); `kit toolspec` declares `"1.1"`:

- **Unset or empty**: the declared version.
- **Well-formed `MAJOR.MINOR` at or below the declared version**:
  the declared version. kit never downgrades: it has no older layout
  to emit, and a 1.0 harness reads 1.1 because the change is
  additive.
- **Well-formed `MAJOR.MINOR` above the declared version**: the
  highest version kit emits that does not exceed the request, so
  `1.1` or `2.0` against a tool declaring `"1.0"` gets `"1.1"`.
- **Malformed**: ignored; the declared version, like every other kit
  env var that fails to parse. No request is refused.

| Declared | Request | Answer |
|----------|---------|--------|
| `"1.0"` | unset, `1.0`, `0.9`, `garbage` | `"1.0"` |
| `"1.0"` | `1.1`, `2.0` | `"1.1"` |
| `"1.1"` (`kit toolspec`) | anything | `"1.1"` |

The variable is read each time the command runs, so set it on the
process you spawn.

## What is and isn't shipped today

Today:

- Manifest schema is published and stable
  (`go/ai/toolspec/spec.go`).
- `kit toolspec` and `<tool> spec` discovery surfaces emit the same
  manifest shape. The `<tool> manifest` subcommand was retired with
  schema 1.1; `--format manifest` remains an alias of
  `--format kit-manifest`.
- The default policy table at `go/ai/toolspec/policy/default.yaml`
  ships embedded; resolve via `policy.Default().Resolve(...)`.
- `adapters.EnforceMCPRequest()` is the runtime gate.
- `kit toolspec policy --file <yaml>` inspects merged tables.

Not yet shipped:

- The `network` axis is not yet populated on individual commands
  (`networkAxisFor` returns NetworkNone today). Once kit ships
  `kit/network` annotations, EnforceMCPRequest reads them
  unchanged.
- The richer `Safety.Permissions []string` vocabulary will plug
  into the gate as a higher-priority key than (side_effect,
  network).

## References

- [toolspec-harness-guide.md](toolspec-harness-guide.md) — discovery, caching, policy resolution
- `go/ai/toolspec/policy/default.yaml` — the table itself
- `go/ai/toolspec/adapters/mcp_enforce.go` — the gate
- `go/ai/toolspec/adapters/mcp.go` — the MCP envelope renderer
- [toolspec-api.md](../reference/toolspec-api.md) — the manifest schema
