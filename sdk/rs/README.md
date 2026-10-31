# rs

Experimental Rust client SDK for hop-top kit.

## Install

```toml
[dependencies]
hop-top-kit = { version = "0.5.0-alpha.6", features = ["cli", "output"] }
```

Nothing is on by default: name the features you use. The version names a prerelease on purpose: a bare `"0.5"` cannot resolve one. Runnable crate examples live in [`examples/`](examples/): [`cli`](examples/cli.rs) (a kit root with output and the logger; `--features cli,config`), [`mcp`](examples/mcp.rs) (commands as MCP tools over stdio; `--features mcp`) and [`serve`](examples/serve.rs) (`--features serve-cli`); `tests/examples_smoke.rs` runs the first two.

## Modules

Paths are relative to `src/`; the [feature table](#features) is the authority on what each pulls in.

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`serve/`](src/serve/README.md), [`mcp/`](src/mcp/README.md) | serve hierarchy and service lifecycle (feature `serve`, `serve-cli` for the `CommandSpec` command); dual-spec MCP surface over a bridged command tree (feature `mcp`) | your CLI hosts long-running services, or MCP clients must call your commands as tools |
| [`output/`](src/output/README.md) | `--format` flag family, `Formatter` trait, registry, `CliError` (feature `output`); shipped formatters in [`builtins/`](src/output/builtins/README.md) | a command renders one payload as table, json or yaml |
| [`rpc/`](src/rpc/README.md) | Connect, gRPC and gRPC-Web on one port (HTTP/1.1 and h2c), request-id/log/authenticate interceptors over kit-rs authn, `crud.v1.EntityService` (feature `rpc-server`, native) | a tool serves procedures to Connect, gRPC or gRPC-Web clients |
| [`socket/`](src/socket/README.md) | unix-socket JSON-lines transport with peer-uid auth and idempotency replay, a `serve` service (feature `socket`, unix only) | a tool must answer a local client over a `0600` socket |
| [`parity/`](src/parity/mod.rs) | typed loader for `contracts/parity/parity.json` (status symbols, spinner, anim, help sections, verbosity, streams) with a drift guard (feature `output`) | a CLI needs the cross-language TUI constants |
| [`bus/`](src/bus/README.md) | in-process event bus with topic grammar (feature `bus`); peers over WebSocket in [`bus/network/`](src/bus/network/README.md) (feature `bus-network`) | components publish or subscribe to named events, across processes too |
| [`domain/`](src/domain/README.md) | state machine with veto-able pre and fire-and-forget post transition events; the `Repository` port (feature `domain`) | an entity has a lifecycle others react to |
| [`breaker/`](src/breaker/README.md) | circuit breaker, MaxOps/MaxBytes budgets, bursty rate limiter (feature `breaker`) | a tool must stop itself after failures, a budget or a rate |
| [`sync/`](src/sync/README.md) | local-first replication: hybrid logical clock, diffs, named remotes with sync modes and filters, last-write-wins and custom merges, memory transport and replicator with status (feature `sync`; `sync-http` for the HTTP transport) | two stores of the same entities must converge without a server |
| [`config/`](src/config/README.md) | layered YAML config: load, get/set/unset keeping comments, hot reload (feature `config`; `config-signal`, `config-pkl`) | a CLI reads or writes its config files |
| [`consent/`](src/consent/README.md) | per-capability consent grants: check, request through a prompter, use with audit, revoke; grants file, audit log and bus relay (feature `consent`; `consent-file` for the files) | a tool reads the screen, camera, mic, clipboard or files for the user |
| [`notify/`](src/notify/README.md) | severity convention, filter and retry + dead-letter sink decorators, webhook, email (SMTP) and desktop sinks over the bus `Sink`, a port of `go/runtime/notify` (feature `notify`, wasm-safe core; `notify-webhook`, `notify-email`, `notify-os` for the sinks) | a bus event must reach Slack, a mailbox or the operator's screen |
| [`sideeffect/`](src/sideeffect/README.md) | the `Fs`, `Http`, `Bus` and `Exec` seams with real, dryrun and testfake implementations, the dry-run flag on a context and a dry-run bus publisher, a port of `go/runtime/sideeffect` (feature `sideeffect`, wasm-safe; `sideeffect-real`, `sideeffect-http` for the production ones) | a command mutates files, calls an API, publishes or spawns and must preview or be tested |
| [`httpcache/`](src/httpcache/README.md) | HTTP response cache over a `kv` TTL store (feature `httpcache`) | responses must be reused across runs |
| [`blob/`](src/blob/README.md), [`sqlstore/`](src/sqlstore/README.md) | object storage over a backend trait, local filesystem only (feature `blob`); typed JSON store over `sqldb` with backup and encryption (feature `sqlstore`) | you store opaque files or artifacts, or a CLI keeps typed records locally |
| [`id/`](src/id/README.md), [`timeutil/`](src/timeutil/README.md) | TypeID primitive (feature `id`); `since` and `until` relative-date parsing (feature `timeutil`) | you mint or parse prefixed identifiers, or a flag takes a human time expression |
| [`policy/`](src/policy/README.md) | declarative CEL guard rules vetoing the state machine's pre-transition and the entity pre-events, deny-overrides, typed `PolicyDeniedError`, the stage ruleset, a port of `go/runtime/policy` (feature `policy`, wasm-safe) | who may do what is rules in a file, not code |
| [`identity/`](src/identity/README.md), [`redact/`](src/redact/README.md) | local Ed25519 identity, a port of `go/core/identity`: keypair with PEM, EdDSA JWT sign/verify, HKDF key derivation, NaCl secretbox, on-disk store (feature `identity`; core wasm-safe, store native only); secret and PII redaction with gitleaks rule semantics and the default corpus embedded, a port of `go/core/redact` (feature `redact`, wasm-safe) | a tool needs a local keypair, signed tokens or encryption at rest, or text may carry credentials before it is logged, sent or shown |
| [`blob/`](src/blob/README.md) | object storage over a backend trait with content-type sidecars and optional `Stater`, local filesystem only (feature `blob`) | you store opaque files or artifacts |
| [`telemetry/`](src/telemetry/README.md) | consent-gated usage events, redaction, sinks (feature `telemetry`) | you record usage under user consent |
| [`upgrade/`](src/upgrade/README.md) | versioned state migrations: semver-ordered steps per schema, backup, rollback, version files, and the offline-aware release check with cache and notice (feature `upgrade`) | stored data must move forward when a new build ships, or the tool should announce a newer release |
| [`compliance/`](src/compliance/README.md) | the 12-factor AI CLI compliance check over a toolspec: YAML loader, factors 1 to 13 (static pass), text and JSON report (feature `compliance`) | a toolspec must be graded in CI or by a `doctor` command |
| [`sqldb.rs`](src/sqldb.rs), [`kv.rs`](src/kv.rs) | SQLite connection setup, pragmas, numbered migrations (feature `sqldb`); byte-keyed `Store` / `TtlStore` traits over it (feature `kv`) | you open a database directly, or need small keyed values with expiry |
| [`authn/`](src/authn/README.md) | bearer-token (JWT) and API-key authentication: own JWS verifier, key sets, claims, typed refusals, a port of `go/transport/authn` (feature `authn`, wasm-safe; `authn-rsa` for RS*/PS*, `authn-apikeys` for the kv-backed keys) | a service must say who is calling |
| [`llm/`](src/llm/README.md) | provider-neutral LLM core: messages, tool calls and tool choice, streaming with opt-in tool-call and usage events, key resolution (sync or async secret store), hermetic resolve, registry, fallback client with metadata-only bus events (feature `llm`; content redaction with `redact`); adapters for Anthropic, OpenAI and compatible hosts, Gemini, Ollama, Triton and RouteLLM (features `llm-anthropic`, `llm-openai`, `llm-google`, `llm-ollama`, `llm-triton`, `llm-routellm`; see the [adapter table](src/llm/README.md)), the model picker with the llm.yaml pool (`llm-picker`, wasm-safe) and the router engine (`llm-router`) | your tool calls a model by `scheme://model` URI |
| [`api.rs`](src/api.rs), [`uri.rs`](src/uri.rs) | JSON API client over the guarded client (feature `api`); URI facade delegating to `hop-top-cite` (feature `uri`) | you call an HTTP API from a kit CLI, or parse and format kit URIs |
| [`api_server/`](src/api_server/README.md) | the REST server: axum router in kit's middleware stack (request id, client address, redacted access log, recovery, security headers, health, Host and Origin checks, CORS, body limit, gzip, rate limit, auth, bus events), OpenAPI 3.1 emitter, SSE, the `/v1/commands` projection of a kit CLI root (with feature `cli`), served as a `serve::Service`; a port of `go/transport/api` (feature `api-server`, native only) | a tool serves HTTP |
| [`netpolicy/`](src/netpolicy/README.md) | `--offline` marker, `netpolicy.allow_hosts` allowlist, loopback and dial checks (feature `netpolicy`, `url` only, wasm-safe); the guarded reqwest client (feature `api`) | a request or a dial must honour `--offline` or an egress allowlist |
| [`tui/`](src/tui/README.md) | progress events (JSONL or human), themed bar, spinner, status lines, confirm prompt with a non-interactive fallback, app shell frame; plain ANSI on std, a port of `go/console/progress` and `go/console/tui` (feature `tui`) | a command reports phases on stderr or asks yes/no |
| [`cli/`](src/cli/README.md) | kit CLI root (a kit-owned `CommandSpec` builder; clap stays adapter-internal): persistent flags, `-v/--version`, help groups, `Root::execute`, `-C` / `-c`, `--confirm` / `--max-ops` / `--policy` gates, `--dry-run`, flag validators, `Root::validate`, the alias store, `config path|paths`, flag-error envelopes, autocorrect, kit-rendered help (groups, `--help-all`, `help <topic>`, a color `Theme`), the `-V`/`--quiet` logger, the `--force` safety guard, a kit-owned command model, and shell completion: `completion bash\|zsh\|fish\|powershell`, `completion fig\|usage` specs, `__complete`, root aliases, flag enums, completers (feature `cli`) | you build a kit CLI or offer shell completion |

## Features

Every module is feature-gated and `default = []`: name no features, compile **zero** dependencies.

| Feature | Pulls in | Normal deps |
|---------|----------|-------------|
| `netpolicy` (`api`); `blob` | nothing (+ `reqwest`, `serde`, `serde_json`, `thiserror`); `thiserror` | 0 (113); 6 |
| `netpolicy` (`api`) | `url` (+ `reqwest`, `serde`, `serde_json`, `thiserror`) | 44 (113) |
| `blob` | `thiserror`, `serde_json` | 11 |
| `sqldb` (`kv`) | `rusqlite` (bundled SQLite), `thiserror` (`kv`: nothing of its own) | 12 (12) |
| `bus` (`domain`, `breaker`, `bus-network`, `bus-network-axum`, `bus-network-webpki-roots`) | `serde`, `serde_json` (`domain`, `breaker`: nothing of their own, wasm-safe; + `netpolicy`, `tokio` rt/net/time/sync/macros, `tokio-tungstenite`, `futures-util`, `getrandom`, `rustls` (ring), `rustls-native-certs`; + `axum`, `hyper`, `hyper-util`; + `webpki-roots`) | 14 (14, 14, 60, 76, 61) |
| `sync` (`sync-http`) | `domain` + `base64` (`sync-http`: `api`, `tokio` rt and time, nothing new beyond `api`; native only) | 15 (113) |
| `upgrade`; `consent` (`consent-file`) | `bus` + `thiserror` (wasm-safe core; runner off wasm32); `bus` — nothing of its own, wasm-safe (+ `config`, `id`; native only) | 16; 11 (27) |
| `notify` (`notify-webhook`, `notify-email`, `notify-os`) | `bus` — nothing of its own, wasm-safe core (+ `api`, `breaker`, `redact`, `tokio` rt/net/time/sync/macros; + `breaker`, `redact`, `netpolicy`, `base64`, `rustls` (ring), `rustls-native-certs`; + `breaker`, `redact`; sinks native only) | 13 (153, 47, 29) |
| `sideeffect` (`sideeffect-real`, `sideeffect-http`) | `bus` — nothing of its own, wasm-safe (`sideeffect-real`: nothing of its own, native only; `sideeffect-http`: + `api`, the guarded HTTP client) | 13 (13, 138) |
| `config` (`config-pkl`, `config-signal`) | `bus` + `saphyr-parser`, `thiserror` (+ `regex`; + `tokio` rt, signal, sync) | 18 (22, 25) |
| `authn` (`authn-rsa`, `authn-apikeys`) | `serde`, `serde_json`, `sha2`, `subtle`, `base64`, `p256`, `p384`, `p521`, `ed25519-dalek` (+ `ring`; + `kv` and `getrandom`; wasm-safe core) | 49 (52, 58) |
| `identity`; `redact` | `ed25519-dalek`, `crypto_secretbox`, `hkdf`, `sha2`, `base64`, `serde`, `serde_json`, `thiserror` (+ `getrandom` on native targets; core wasm-safe); `regex`, `regex-syntax`, `serde`, `serde_json`, `sha2` | 60; 25 |
| `authn-remote` | `authn` + `api` + `url`, `tokio` (rt) (native only) | 186 |
| `api-server` (`api-server-tls`, `api-server-ws`, `api-server-idempotency`) | `serve` + `authn` + `bus` + `breaker` + `redact` + `axum`, `tower`, `tower-http`, `futures-util`, `getrandom`, `hyper`, `hyper-util` (native only; + `rustls` (ring), `tokio-rustls`; + `tokio-tungstenite`; + `kv`, `base64`) | 111 (118, 118, 115) |
| `webhook` (`webhook-sha1`) | `api-server` + `kv` + `hmac`, `subtle`, `sha2`, `base64` (native only; + `sha1`, legacy senders opt-in) | 115 (116) |
| `output` | `serde`, `serde_json`, `serde_norway`, `comfy-table`, `thiserror` | 22 |
| `sqlstore` (`sqlstore-blob`, `sqlstore-encrypt`) | `sqldb` + `serde`, `serde_json` (+ `blob`, nothing of its own; + `crypto_secretbox`, `hkdf`, `sha2`) | 22 (22, 50) |
| `id`; `timeutil` | `type-safe-id`, `uuid`, `serde`, `thiserror`; `chrono`, `interim`, `thiserror` | 25; 19 |
| `httpcache` | `kv` + `serde`, `serde_json`, `sha2`, `base64` | 33 |
| `cli` (`cli-permissions`); `tui` | `output` + `clap` (+ `cel`, `serde_json`); nothing (std only) | 35 (76); 0 |
| `compliance` | `output` | 22 |
| `rpc-server` | `api-server` + `connectrpc`, `buffa`, `buffa-types`, `serde` (native only) | `api-server` + 31 (171) |
| `socket` | `serve` + `serde`, `getrandom`, tokio `net`/`io-util` (unix only) | 0 beyond `serve` |
| `serve` (`serve-cli`) | `output` + `tokio`, `serde_json` (+ `cli`) | 37 (50) |
| `mcp`; `uri` | `rmcp` (default-features off) + `serde`, `serde_json`; `hop-top-cite` | 39; 41 |
| `policy` | `domain` + `cel`, `serde`, `serde_json`, `serde_norway` (`PolicyDeniedError::to_cli_error` with `output`) | 48 |
| `llm` (`llm-anthropic`, `llm-openai`, `llm-google`, `llm-ollama`, `llm-triton`, `llm-routellm`; `llm-picker`; `llm-router`) | `config` + `netpolicy` + `url`, `sha2` (+ `api`, `base64`, `tokio` time via `llm-http`; native only; `llm-routellm` adds `llm-openai`; `llm-picker`: nothing of its own, wasm-safe; `llm-router`: `llm-triton` + `llm-picker`) | 53 (114) |
| `telemetry` | `api` + `serde_norway`, `sha2`, `getrandom`, `dirs`, `regex` | 131 |

Counts are non-dev crates, root excluded, from `cargo tree --no-default-features --features <feature> -e normal`. `api` and `telemetry` are heaviest; bundled `rusqlite` needs a C compiler. Native-only sinks and adapters, runtime and C-compiler needs: [build notes](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/rs-sdk.md#build-notes).

## Rust versions

The minimum supported Rust version (MSRV) is **1.89**, declared as `rust-version` in `Cargo.toml` and tested in CI with `cargo test --all-features` on that exact toolchain, next to the pinned development toolchain (`rust-toolchain.toml`) and current stable.

Policy: the MSRV is the lowest version the crate and its locked dependencies compile on, not a promise to trail stable. Raising it is a breaking change: it ships in a minor release (a major one from 1.0) and is called out in the changelog. A dependency bump that raises the floor either waits for such a release or is held back. After a bump, set `rust-version` to the highest `rust-version` in `cargo metadata --all-features` and confirm the MSRV job passes.

## See also

- [Rust SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/rs-sdk.md): [crate-wide contract](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/rs-sdk.md#crate-wide-contract), serve, URI facade, output, MCP mount, storage and the cross-process gate, httpcache, offline, telemetry, bus, config, authentication, REST server; [Serve lifecycle contract](https://github.com/hop-top/poly-kit/blob/main/docs/contracts/serve-lifecycle.md), [CLI parity guide](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/cli-parity-guide.md), [Serve MCP from any SDK](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/serve-mcp-from-any-sdk.md)
