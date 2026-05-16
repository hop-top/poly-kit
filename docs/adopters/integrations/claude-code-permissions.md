# Configure Claude Code permissions via the kit-toolspec contract

Status: published
Track: `kit-toolspec-ai-harness-contract`
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

ADR-0022 defines a manifest consumption contract. A harness:

1. Discovers a kit-powered CLI's manifest via `<tool> manifest` or
   `<tool> spec --format kit-manifest`.
2. Reads the per-leaf `side_effect` (and, post-safety-ladder, the
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

Harnesses signal their max-supported schema version via the env var
`KIT_TOOLSPEC_SCHEMA`:

```sh
KIT_TOOLSPEC_SCHEMA=1.0 tlc manifest
```

Today only `"1.0"` exists. When the safety-ladder track lands `"2.0"`
(richer side-effect enum + network axis), pinning `1.0` keeps your
harness on the legacy vocabulary while you migrate. See ADR-0022 §3
for the negotiation rules.

## What is and isn't shipped today

Today:

- Manifest schema is published and stable
  (`go/ai/toolspec/spec.go`).
- `kit toolspec`, `<tool> spec`, and `<tool> manifest` discovery
  surfaces all work and emit identical JSON.
- The default policy table at `go/ai/toolspec/policy/default.yaml`
  ships embedded; resolve via `policy.Default().Resolve(...)`.
- `adapters.EnforceMCPRequest()` is the runtime gate.
- `kit toolspec policy --file <yaml>` inspects merged tables.

Pending kit-toolspec-safety-ladder:

- The `network` axis is not yet populated on individual commands
  (`networkAxisFor` returns NetworkNone today). Once kit ships
  `kit/network` annotations, EnforceMCPRequest reads them
  unchanged.
- The richer `Safety.Permissions []string` vocabulary will plug
  into the gate as a higher-priority key than (side_effect,
  network); ADR-0022 documents the upgrade path.

## References

- ADR-0022 — protocol, version negotiation, default policy
- `go/ai/toolspec/policy/default.yaml` — the table itself
- `go/ai/toolspec/adapters/mcp_enforce.go` — the gate
- `go/ai/toolspec/adapters/mcp.go` — the MCP envelope renderer
- `~/.ops/docs/cli-conventions-with-kit.md` §13 — manifest schema lock
