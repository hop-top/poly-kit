# rs

Experimental Rust client SDK for hop-top kit.

## Install

```toml
[dependencies]
hop-top-kit = { version = "0.5", features = ["cli", "output"] }
```

Nothing is on by default: name the features you use. Runnable crate examples live in [`examples/`](examples/).

## Modules

Paths are relative to `src/`; the [feature table](#features) is the
authority for what each feature pulls in.

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`serve/`](src/serve/README.md) | serve hierarchy and service lifecycle (feature `serve`, `serve-cli` for the clap command) | your CLI hosts long-running services |
| [`output/`](src/output/README.md) | `--format` flag family, `Formatter` trait, registry, `CliError` (feature `output`) | a command renders one payload as table, json or yaml |
| [`output/builtins/`](src/output/builtins/README.md) | the shipped formatters and their `--format-opt` keys | you need a formatter option or a custom formatter |
| [`bus/`](src/bus/README.md) | in-process event bus with topic grammar (feature `bus`) | components publish or subscribe to named events |
| [`mcp/`](src/mcp/README.md) | dual-spec MCP surface over a bridged command tree (feature `mcp`) | MCP clients must call your commands as tools |
| [`sqlstore/`](src/sqlstore/README.md) | typed JSON store over `sqldb` with backup and encryption (feature `sqlstore`) | a CLI keeps typed records locally |
| [`httpcache/`](src/httpcache/README.md) | HTTP response cache over a `kv` TTL store (feature `httpcache`) | responses must be reused across runs |
| [`blob/`](src/blob/README.md) | object storage over a backend trait, local filesystem only (feature `blob`) | you store opaque files or artifacts |
| [`id/`](src/id/README.md) | TypeID primitive (feature `id`) | you mint or parse prefixed identifiers |
| [`telemetry/`](src/telemetry/README.md) | consent-gated usage events, redaction, sinks (feature `telemetry`) | you record usage under user consent |
| [`timeutil/`](src/timeutil/README.md) | `since` and `until` relative-date parsing (feature `timeutil`) | a flag takes a human time expression |
| [`sqldb.rs`](src/sqldb.rs) | SQLite connection setup, pragmas, numbered migrations (feature `sqldb`) | you open a database directly |
| [`kv.rs`](src/kv.rs) | byte-keyed `Store` / `TtlStore` traits over SQLite (feature `kv`) | you need small keyed values with expiry |
| [`netpolicy.rs`](src/netpolicy.rs) | `--offline` marker and the guarded reqwest client (feature `api`) | a request must honour `--offline` |
| [`api.rs`](src/api.rs) | JSON API client over the guarded client (feature `api`) | you call an HTTP API from a kit CLI |
| [`uri.rs`](src/uri.rs) | URI facade delegating to `hop-top-cite` (feature `uri`) | you parse or format kit URIs |
| [`cli.rs`](src/cli.rs), [`tui.rs`](src/tui.rs) | placeholders, empty | never; both are reserved |

## Features

Every module is feature-gated and `default = []`, so a dependant that names
no features compiles the crate with **zero** transitive dependencies. Add
only what you use:

| Feature | Pulls in | Normal deps |
|---------|----------|-------------|
| `blob` | `thiserror` | 6 |
| `bus` | `serde`, `serde_json` | 11 |
| `sqldb` | `rusqlite` (bundled SQLite), `thiserror` | 12 |
| `kv` | `sqldb` — nothing of its own | 12 |
| `sqlstore` | `sqldb` + `serde`, `serde_json` | 19 |
| `httpcache` | `kv` + `serde`, `serde_json`, `sha2`, `base64` | 29 |
| `sqlstore-encrypt` | `sqlstore` + `crypto_secretbox`, `hkdf`, `sha2` | 42 |
| `mcp` | `rmcp` (default-features off) + `serde`, `serde_json` | — |
| `serve` | `output` + `tokio`, `serde_json` | 59 |
| `serve-cli` | `serve` + `cli` (`clap`) | 77 |

`mcp` is the heaviest feature: `rmcp` carries an async runtime in its tree
even with default features off. Counts are non-dev crates, reproducible
with `cargo tree --no-default-features --features <feature> -e normal`.

`sqlstore-blob` adds blob-backed backup/restore and pulls in only what
`blob` already carries. `rusqlite` uses the `bundled` feature, so SQLite is
compiled from source and no system `libsqlite3` is required, at the cost of
a C compiler on the build host and a slower first build.

## Contract

- `serve <service>` runs a named service even when disabled; a `serve` the clap root already owned is replaced, never doubled.
- `output` ships `table`, `json` and `yaml` only; `csv` and `text` are not implemented in Rust.
- Column order comes from an explicit `ColumnSpec` list; `--cols` reorders as well as selects; `header` must equal `key`.
- MCP exposure is default-closed: the default policy blocks every destructive leaf on every remote surface.
- Telemetry is default-denied: nothing emits without both a granted consent decision and a non-`off` mode.

## See also

- [Rust SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/rs-sdk.md):
  serve, the URI facade, output rules, the MCP mount, storage and the
  cross-process gate, the httpcache wire contract, telemetry, the bus
- [Serve lifecycle contract](https://github.com/hop-top/poly-kit/blob/main/docs/contracts/serve-lifecycle.md), [CLI parity guide](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/cli-parity-guide.md), [Serve MCP from any SDK](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/serve-mcp-from-any-sdk.md)
