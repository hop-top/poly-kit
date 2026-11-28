---
name: hop-top-kit-rs
description: Build a CLI or service in Rust with the hop-top-kit crate, covering Cargo features per module, the kit CLI root, layered config, output formats, MCP, serve, upgrade migrations, wasm32. Use when a Rust project depends on or should adopt hop-top-kit, when picking its Cargo features, or when checking what the Rust port covers before promising a capability.
license: MIT
metadata:
  version: "0.5.0-alpha.9" # x-release-please-version
  min_version: "0.5.0-alpha.9"
---

# hop-top-kit-rs

The `hop-top-kit` crate (Rust path `hop_top_kit`): kit's Rust port. Every module is behind a Cargo
feature and nothing is on by default, so choosing features comes first. The API is sync except
`serve` and the network features (tokio).

## Use another port when

Rust releases are alpha prereleases: a task needing a stable release goes elsewhere
([other ports](references/other-ports.md), with the router skill).

<!-- skills-sync:begin coverage -->
Tier: experimental.

- CLI and console. Lacks: Markdown rendering, Autocorrect, Config path commands, Fuzzy matching, Process status, Wizard, Progress events.
- Config, storage and secrets. Lacks: Project registry, Secrets.
- Serving and transports. Lacks: Service command group, Transport service seam, Served observability, Bridge payloads.
- Events, jobs and runtime. Lacks: Job queue, Peer mesh.
- Guardrails and trust. Lacks: Path scope, Operating mode, Provenance, Audit log, Avatars.
- AI and agents. Lacks: Toolspec, Command reflection, Agent CLI invocation, Extensions.
- Conformance and integrations. Has only: nothing.
<!-- skills-sync:end coverage -->

- Capability `none` here, `kit init` scaffolding, or the `kit` binary: Go skill, or the binary as a
  sidecar. Scaffolding is the Go binary's
  [`kit init`](https://github.com/hop-top/poly-kit/blob/next/cmd/kit/README.md); run it with
  `--dry-run` first to see what it writes for Rust.
- Capability `partial` or `unverified` and the task depends on the gap: read the area reference,
  test the behaviour, or use Go.
- The project is already Go (or TypeScript, Python): stay while its matrix cells cover the task; add
  this crate only for a separate Rust component.

## Install

```toml
[dependencies]
hop-top-kit = { version = "0.5.0-alpha.9", features = ["cli", "config"] } # x-release-please-version
```

Give the full prerelease version: a bare `"0.5"` cannot resolve one. Rust 1.89 or newer.

## Pick features

| You need | Features | Weight |
|----------|----------|--------|
| CLI root, `--format`, help, completion | `cli` (adds `output`); add `config` for `-c` layering | clap, serde |
| Layered YAML config | `config`; `config-signal` (unix); `config-pkl` | light |
| MCP tools over stdio | `mcp` | serde |
| Long-running services | `serve`; `serve-cli` adds the command | tokio |
| REST, TLS, ws, RPC | `api-server`, `api-server-tls`, `api-server-ws`, `rpc-server` | heavy |
| Migrations, versioned state | `upgrade`, `upgrade-sqlite`, `upgrade-fsdir`, `upgrade-postgres` | below |
| HTTP honouring `--offline` | `api`, or `netpolicy` alone | reqwest, tokio |

`upgrade` is the registry, runner and config-file driver: a handful of crates. `upgrade-sqlite` adds
bundled SQLite (needs a C compiler); `upgrade-fsdir` adds `tar` and `flate2`; `upgrade-postgres`
adds the `postgres` client and its async stack, several times the others, over a `postgres::Client`
you pass in (TLS is yours). Recount any feature with
`cargo tree --no-default-features --features <f> -e normal`.

## Smallest program

[`cli-root.rs`](assets/snippets/cli-root/cli-root.rs): a kit root with a rendered list command. Test
with `cargo test --features cli,config`; test each feature alone with
[`check-rs-features.sh`](https://github.com/hop-top/poly-kit/blob/next/scripts/check-rs-features.sh).

## Traps

- Types are kit-owned (`CommandSpec`, `FlagSpec`, `Invocation`); no clap type appears in your code.
- `Root::run(argv, out, err)` returns the exit code; you call `exit`. Run examples with their
  features: `cargo run --example cli --features cli,config`.
- `cli` does not pull `config`; `Invocation::layer_config` needs it.
- tokio carries no runtime flavour: add your own with `rt-multi-thread` and `macros` for `serve`.
- `ring` (`bus-network`, `notify-email`, `authn-rsa`) needs a C compiler.

<!-- skills-sync:begin version -->
## Versions

Written for 0.5.0-alpha.9. # x-release-please-version
Expects 0.5.0-alpha.9 or newer.

Installed: `cargo tree -i hop-top-kit --depth 0`; `scripts/detect-port.sh` shows what the manifest
pins. Older than expected: tell the user, upgrade (`cargo update -p hop-top-kit`). Newer than
written for: this copy may be stale; update it, then re-read it. Update by install channel, then
restart the harness: Claude Code plugin `claude plugin marketplace update hop-top`, then
`claude plugin update hop-top-kit-rs@hop-top` (marketplace repository `hop-top/skillset`); gym,
`npx skills` or by hand: take `skills/hop-top-kit-rs` again from `hop-top/kit-rs` at `main`.
<!-- skills-sync:end version -->

## Open when

| File | Open when |
|------|-----------|
| `references/coverage.md` | a capability's status or its Evidence must be read before promising it |
| `references/other-ports.md` | switching port, or a capability is Go-only |
| `references/cli-and-console.md` | building the CLI root, help, flags, aliases, completion, progress |
| `references/config-storage-secrets.md` | config files, XDG paths, key-value, blobs, SQLite stores |
| `references/serving-and-transports.md` | serve lifecycle, MCP, REST, RPC, sockets, auth, the feature table |
| `references/events-jobs-runtime.md` | bus, state machines, sync, notifications, telemetry |
| `references/guardrails-and-trust.md` | breaker, redaction, consent, identity, upgrade migrations, retry |
| `references/ai-and-agents.md` | calling models, routing, the 12-factor check |
| `references/wasm32.md` | building for wasm32: which features CI builds and what is compiled out |
| `scripts/detect-port.sh` | the project's kit dependency or language is unknown |
| `assets/snippets/cli-root/cli-root.rs`, `assets/snippets/cli-root/cli-root.expected` | writing the first command, or checking its output |
| `assets/snippets/config-layers/config-layers.rs`, `assets/snippets/config-layers/config-layers.expected` | loading a typed config from files and `-c` overrides |
| `assets/snippets/mcp-tools/mcp-tools.rs`, `assets/snippets/mcp-tools/mcp-tools.expected` | exposing commands as MCP tools over stdio |
