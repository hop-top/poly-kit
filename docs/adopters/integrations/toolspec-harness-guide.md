# Consume kit's toolspec contract from your harness

Status: published
Track: `kit-toolspec-ai-harness-contract`
Audience: harness implementers (MCP hosts, agent frameworks, IDE
extensions, custom Claude Code adapters).

## What this lets you build

A harness that:

1. Discovers any kit-powered CLI's capability manifest by invoking
   one well-known subcommand.
2. Resolves per-command (side-effect, network) tuples through an
   opinionated default policy table — or a per-host overlay — into
   `auto-allow | prompt | deny`.
3. Inherits policy for new kit-powered CLIs without code changes.

The contract is anchored in [ADR-0022](../adr/0022-toolspec-ai-harness-contract.md);
the artefact set this guide draws from lives under
`go/ai/toolspec/`.

## Step 1 — Discover the manifest

Three discovery surfaces, in priority order:

| Surface                                | When to use                                                               |
|----------------------------------------|---------------------------------------------------------------------------|
| `<tool> spec --format kit-manifest`    | Default. Every kit-powered CLI gains this via `cli.RegisterSpecCommand`.  |
| `<tool> manifest`                      | Adopter opted into the shorter spelling (see adopter guide).              |
| `kit toolspec`                         | Bootstrap manifest for the kit binary itself; protocol-discovery anchor.  |

All three emit identical `toolspec.Manifest` JSON. The output is
self-describing — the top-level `schema_version` field is your
single source of truth for breaking changes.

```sh
$ tlc spec --format kit-manifest | jq '.schema_version'
"1.0"

$ tlc spec --format kit-manifest | jq '.commands[0]'
{
  "path": ["tlc", "task", "list"],
  "short": "List tasks",
  "side_effect": "read",
  "idempotent": "yes",
  "flags": [...]
}
```

Negotiate the schema version by setting `KIT_TOOLSPEC_SCHEMA` in the
process env before invoking the binary:

```sh
$ KIT_TOOLSPEC_SCHEMA=1.0 tlc spec --format kit-manifest
```

Today only `"1.0"` exists, so the env var is forward-compatibility
plumbing — pin it once you start consuming the contract so future
schema bumps don't surprise you.

## Step 2 — Cache by binary fingerprint

Manifests are derived from the **running binary**, not the
version string. Two `tlc` binaries with the same git tag can ship
different manifests (different build tags, conditional command
registration). Cache the manifest JSON keyed on:

```text
sha256(binary path) || schema_version
```

Stale caches keyed on `version` will silently miss new commands.

## Step 3 — Resolve policy

Every harness needs a function:

```text
(manifest, command_path) -> auto-allow | prompt | deny
```

Kit ships the canonical implementation as a Go package. Pseudocode
for any language:

```text
table = LoadOrDefault(custom_overlay_path)            // YAML overlay optional
leaf  = manifest.commands.find(c => c.path == path)
if leaf is None:
    return DENY("command not advertised in manifest")
side_effect = leaf.side_effect or "destructive"        // fail safe
network     = leaf.network or "none"                   // stub today
return table.resolve(side_effect, network)
```

Go consumers use `policy.LoadOrDefault` + `adapters.EnforceMCPRequest`
verbatim:

```go
import (
    "hop.top/kit/go/ai/toolspec/adapters"
    "hop.top/kit/go/ai/toolspec/policy"
)

table, _ := policy.LoadOrDefault("")    // "" → embedded default
env := adapters.EnforceMCPRequest(manifest, []string{"tlc", "task", "list"}, table)
```

`env.Decision.Action` is one of the three constants in the
`policy` package; `env.Decision.Reason` is the rationale string
the harness should surface to the user.

## Step 4 — Render in your harness's permission shape

The contract gives you an action + a reason; you translate to your
own permission vocabulary. Examples:

- **Claude Code** — `permissions.allow` / `permissions.deny` arrays
  with command pattern strings. See
  [claude-code-permissions.md](claude-code-permissions.md) for the
  worked example.
- **MCP host** — JSON-RPC error envelope with code -32099 on deny;
  proceed silently on auto-allow; raise a UI prompt on prompt. The
  `adapters.EnforceMCPRequest` return value already includes a
  ready-to-marshal `MCPError` for the deny case.
- **Cursor / IDE** — translate to whatever your settings UI accepts;
  the policy decision is shape-independent.

## Step 5 — Allow user overrides

The default table is opinionated. Some users want stricter rules
(prompt every read in production); some want looser rules (auto-allow
write+local for trusted environments). Accept a `--policy <file>`
flag (or its harness-equivalent) and pass the path through
`policy.LoadOrDefault(path)`. Overlay rules win on collision; default
rules fill the gaps.

## Common pitfalls

- **Auto-allow without checking the manifest.** Don't. The gate's
  fail-safe default is ActionPrompt for any unknown tuple, but if
  your harness skips the check entirely, you've defeated the
  contract. Always run EnforceMCPRequest (or its language
  equivalent).
- **Caching by version string.** Prefer binary fingerprint; see
  Step 2.
- **Hard-coding action constants.** Drift-prone. Read them from
  `policy.Action` constants (or the equivalent in your binding)
  so renames surface at compile time.
- **Treating empty side_effect as auto-allow.** The gate maps it
  to destructive (most-restrictive class) so unannotated commands
  fail safe. Don't second-guess this.

## Where to find things

- ADR-0022 — protocol contract
- `go/ai/toolspec/spec.go` — `Manifest` / `ManifestCommand` types
- `go/ai/toolspec/policy/default.yaml` — the embedded default
- `go/ai/toolspec/policy/policy.go` — `Table.Resolve`, `Merge`,
  `LoadOrDefault`
- `go/ai/toolspec/adapters/mcp_enforce.go` — `EnforceMCPRequest`
- [adopter publishing guide](toolspec-adopter-guide.md) — for the
  other side of the contract (publish your tool's manifest)
