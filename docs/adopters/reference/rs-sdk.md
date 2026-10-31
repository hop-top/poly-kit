# Rust SDK Reference

> Every long-form surface of the experimental `hop-top-kit` crate
> (`sdk/experimental/rs`): serve, the URI facade, output formatting
> rules, the MCP mount, storage, the httpcache wire contract, offline
> enforcement, telemetry and the bus. The crate front page is
> [`sdk/experimental/rs/README.md`](../../../sdk/experimental/rs/README.md),
> which carries install, the module table and the feature matrix.

## Who this is for

Rust authors who already depend on the crate and need the detail its
front page links out to. Every module named here is feature-gated; the
front page's feature table is the authority for what each one pulls in.

## Serve

`serve` is the service lifecycle as a library (registry, resolution,
supervisor, signals, events); `serve-cli` adds the `serve [SERVICE]`
command with `--list`, `--enable`/`--disable` and the timeout flags,
and mounts it on any clap root — no kit-owned root required:

```rust
use hop_top_kit::serve::command::{mount, run, ServeCommandOptions};
use hop_top_kit::serve::{ServiceConfig, ServiceConfigs, ServiceRegistry};

#[tokio::main]
async fn main() {
    let mut registry = ServiceRegistry::new();
    registry.register(std::sync::Arc::new(Api)).expect("wiring"); // Api: impl Service
    let mut configs = ServiceConfigs::new();
    configs.insert("api".to_string(), ServiceConfig::enabled());

    let matches = mount(clap::Command::new("tool")).get_matches();
    if let Some(sub) = matches.subcommand_matches("serve") {
        let mut opts = ServeCommandOptions::new(registry);
        opts.configs = Some(configs);
        std::process::exit(run(sub, opts).await.exit_code);
    }
}
```

`serve` runs every configured and enabled service; `serve api` runs that
one even when disabled; `serve --list` prints them in registration order.
A `serve` the root already owned is replaced, never doubled. See
[`examples/serve.rs`](../../../sdk/experimental/rs/examples/serve.rs) for a service that binds a real
listener, and `docs/contracts/serve-lifecycle.md` for the contract.

## URI facade

The Rust URI facade is experimental and feature-gated:

```toml
[dependencies]
hop-top-kit = { version = "0.5.0-alpha.6", features = ["uri"] } # x-release-please-version
```

`hop_top_kit::uri` delegates to `hop-top-cite` (`0.1.0`); it does not
reimplement URI parsing, action routing, completion, or handler
generation. `hop-top-cite` is published on crates.io.

## Output formatting

`hop_top_kit::output` ships the `table`, `json` and `yaml` built-in
formatters. `csv` and `text` are **not implemented in Rust** — see
*Conformance status* below.

### Column ordering

Go, the reference runtime, reads column order off `table:""` struct tags
in field declaration order. Rust rows are `serde_json::Value` maps with
no declaration order to read, so an explicit `ColumnSpec` list carries
it instead. Five rules bind every runtime.

**1. Default order.** With a `ColumnSpec` slice on
`DispatchOptions::columns` and no `--cols`, the list's order and names
drive every formatter. Payload key order is the fallback used *only*
when no list is supplied.

```rust
use hop_top_kit::output::ColumnSpec;
use serde_json::json;

let rows = json!([
    {"count": 3, "status": "ready", "name": "alpha"},
    {"count": 8, "status": "held",  "name": "beta"}
]);
let spec = vec![
    ColumnSpec::new("name", "name", 9),
    ColumnSpec::new("count", "count", 7),
    ColumnSpec::new("status", "status", 5),
];
```

The payload's own key order is `count, status, name`; the spec wins:

```
 name   count  status
 alpha  3      ready
 beta   8      held
```

**2. `--cols` reorders as well as selects.** The user's sequence beats
the `ColumnSpec` order — `--cols status,name` renders `status` then
`name` — and the same rule holds on the no-spec fallback path:

```
 status  name
 ready   alpha
 held    beta
```

`--format json` follows the identical order. This works because the
crate enables serde_json's `preserve_order` feature; without it a
`serde_json::Map` is a `BTreeMap` and re-sorts keys alphabetically on
insert:

```json
[
  {
    "status": "ready",
    "name": "alpha"
  },
  {
    "status": "held",
    "name": "beta"
  }
]
```

**3. `header == key`.** The two name the same column: the label, the
value matched against `--cols`, and the key read off the row.
`ColumnSpec::new` panics on a mismatch (`column.rs:36-43`) — a
construction-site bug, not a recoverable runtime condition. Go cannot
express the split through a `table:""` tag, so no SDK offers one.

**4. Zero rows emits nothing** — not even a bare header row. Emptiness
is decided by row count, never column count, so supplying a
`ColumnSpec` list never resurrects a header for an empty payload.

**5. `priority` is accepted, stored and ignored.** The hide-on-overflow
behavior it drives is implemented in Go only; the field is kept so specs
stay portable until that feature is ported.

`dispatch` collapses `--cols` and the `ColumnSpec` list into one ordered
list before calling a formatter (`resolve_effective_cols`,
`dispatch.rs:161`), so the `cols` slice a formatter receives is already
final. The `Formatter` trait signature is unchanged: third-party
implementations pick up correct ordering with no code change. That
collapse is sound only because `header == key`.

### Go-vs-payload capability gaps

| Capability | Go (reference) | rs | py / ts | php |
|---|---|---|---|---|
| Column order source | `table:""` tags, declaration order | `ColumnSpec` list order | `ColumnSpec` list order | `ColumnSpec` list order |
| `priority` hide-on-overflow | implemented | accepted, stored, ignored | accepted, stored, ignored | accepted, stored, ignored |
| `header != key` | inexpressible via `table:""` | panics in `ColumnSpec::new` | rejected at construction | rejected at construction |
| json/yaml key order | follows resolved order | follows resolved order (`preserve_order`) | follows resolved order | follows resolved order |
| `--cols` reorders | yes | yes | yes | yes |
| Built-in formats | `table`, `json`, `yaml`, `csv`, `text` (+ `human`) | `table`, `json`, `yaml` | all five | `table`, `json`, `yaml` |
| Ordered columns on the template path | `.Cols` | **none** | `cols` | `{*}` |

Go's inability to express `header != key` is the *reason* rule 3 is
universal: no SDK may carry a capability the reference cannot mirror.

### Conformance status

Rust satisfies all five ordering rules on the formatter path, as does Go
across all five formats. The cross-runtime fixtures under
`sdk/tests/cross-lang/` execute the contract against every runtime. Two
gaps are open:

- **`--template` has no ordered-column affordance in Rust.** The minimal
  `{key}` substituter (`dispatch.rs:264`) receives the row and not the
  schema, so a template author cannot observe or use `ColumnSpec` order.
  Since `--template` and `--cols` are mutually exclusive, the schema is
  the *only* ordering signal on that path, and Rust currently exposes
  none of it. Go (`.Cols`), Python and TS (`cols`) expose an iterable
  column list; PHP has a `{*}` placeholder expanding to schema-ordered
  values. The right spelling for Rust is still an open decision — `{*}`
  and an iterable `cols` are not the same affordance, and neither
  minimal substituter can offer an iterable without a real template
  engine.
- **`csv` and `text` are not implemented** in Rust or PHP. Only `table`,
  `json` and `yaml` are portable across all five runtimes, so a caller
  writing against the kit output contract cannot assume `--format csv`
  exists. The fixtures record this as `rs-php-no-csv-text`.

The fixtures compare the **column order re-parsed from each runtime's own
output**, never raw bytes — comfy-table pads cells and PHP's YAML puts
the dash on its own line, so byte comparison was never viable.
Byte-level formatting parity is pinned by each SDK's own unit tests
instead.

## MCP surface

Serves the Model Context Protocol over a bridged command tree, one MCP
tool per runnable leaf. A single mount answers **both** revisions —
`2024-11-05` (handshake) and `2026-07-28` (stateless per-request
envelope) — choosing per request, because the newer revision has no
handshake to negotiate with.

Wire behaviour is pinned by the shared cross-language fixtures in
`sdk/tests/cross-lang/fixtures/mcp-wire.json`, compared as raw bytes by
`tests/mcp_wire_conformance.rs`. Where this README and the fixtures
disagree, **the fixtures win**.

```toml
[dependencies]
hop-top-kit = { version = "0.5.0-alpha.6", features = ["mcp"] } # x-release-please-version
```

`rmcp` supplies the protocol vocabulary only — version constants, the
`-3202x` error codes, reserved `_meta` keys — with `default-features =
false`, so its server/tokio/hyper stack stays out of the tree. This port
hosts via a plain request-to-response function, not rmcp's transports.

### Hosting

```rust
use hop_top_kit::mcp::{Bridge, CallResult, HttpRequest, Leaf, MountOptions, Surface};

let bridge = Bridge::new().leaf(Leaf::new(&["ping"], "Ping the server", |_| {
    Ok(CallResult::ok("pong\n"))
}));
let surface = Surface::mount(bridge, MountOptions::default())?;

let response = surface.call(&HttpRequest::post("/mcp", body));
```

`Surface::call(&self, req: &HttpRequest) -> Response` is the
`tower::Service`-shaped function this crate exports. It is synchronous
and pure, and binds to no HTTP server: wire it to axum, hyper or warp,
and the conformance suite drives it with no socket at all.

`HttpRequest` keeps headers in wire order with duplicates preserved —
the protocol tolerates a header sent twice with identical values but
rejects conflicting duplicates, which a flattened map cannot express.

**Naming trap:** `mcp::Surface` is the mounted handler. The safety enum
is re-exported as `mcp::SurfaceKind` to avoid the collision; every other
kit SDK calls that type `Surface`.

### Hosting over stdio

For a host that spawns the tool (a desktop client, an editor), serve the
same mount over standard input and output:

```rust
use std::io::{stderr, stdin, stdout};

let surface = Surface::mount(bridge, MountOptions::default())?;
surface.serve_stdio(stdin().lock(), stdout(), stderr())?; // the host closed stdin
```

`Surface::serve_stdio(input, output, log) -> io::Result<()>` takes any
`BufRead` input and two `Write + Send` streams. It reads one message per
line and returns once `input` ends and every in-flight reply is written.
Mount options apply as for `Surface::call`. Requests run concurrently,
one scoped thread each (`std::thread::scope`, no async runtime), and
each reply is flushed as it completes. The first read error on `input`
or write error on `output` stops reading, is noted on `log`, and is
returned once in-flight requests finish.

| Input line | Output |
|---|---|
| a request | one reply line carrying its `id`; requests run concurrently, so replies may arrive out of order |
| a notification (no `id`) | nothing, ever |
| `ping` (2024-11-05) | `{"jsonrpc":"2.0","id":…,"result":{}}` |
| not JSON | `-32700`, `"id": null` |
| a JSON array (batch) | `-32600`, `"id": null`: neither revision allows batches over stdio |
| a non-object, or an object with an `id` but no `method` | `-32600` |
| a response (`result` / `error`, no `method`) | nothing; noted on `log` |
| a blank line | skipped |
| a request whose leaf panics | `-32603` `internal error`; the panic message goes to `log` |

Output carries protocol messages only; diagnostics go to `log`.

stdio has no header layer. Era detection uses the body markers alone (the
reserved `_meta` protocolVersion key, `server/discover`), and the
`2026-07-28` header checks (`MCP-Protocol-Version`, `Mcp-Method`,
`Mcp-Name`: V4, V6, V7) do not apply; every other check runs unchanged.
Gates that read headers see none: the mount's `verifier` is called with a
header-less request, so a `kit/auth-required` leaf is refused unless your
verifier admits it, and the `X-Confirm-Token` header gate cannot be
satisfied. A mount with a `confirmation_key` can still confirm over
stdio through the MRTR round-trip, for a `2026-07-28` client that
declares form elicitation.

### Options

`MountOptions` is a plain struct with `Default`, not a builder: `path`
(`/mcp`), `server_name` (`cmdsurface`), `server_version` (`0.0.0`),
`spec_versions` (`None` = both eras), `cache_ttl_ms` (`0`),
`cache_scope` (`Private`), `origin_allowlist` (empty, no check),
`confirmation_key` (`None`), `tasks_enabled` (`false`),
`tool_name_separator` (`.`), `audit` (`None`). The option *set* is
normative across every kit SDK; only the spelling is idiomatic.

`tool_name_separator` joins a leaf path into its tool name: `"_"` names
the leaf `widget add` `widget_add`, for hosts that admit only
`^[A-Za-z0-9_-]{1,64}$`. Calls resolve under the same separator
(`Bridge::resolve_with`, `Bridge::invoke_with`; `Leaf::tool_name_with`
renders one name).

`Surface::mount` returns `Result<Surface, MountError>` rather than
starting a mount that quietly serves nothing:

| Variant | Trigger |
|---------|---------|
| `NoSpecVersions` | `spec_versions: Some(vec![])` |
| `NegativeCacheTtl` | `cache_ttl_ms < 0` |
| `EmptyConfirmationKey` | `confirmation_key: Some(vec![])` |
| `EmptyToolNameSeparator` | `tool_name_separator: String::new()` |
| `ToolNameSeparatorInCommand { command, path, separator }` | a separator other than `.` inside a command name: `data_sync` under `_` would not map back to one leaf |

`MountError` is `Clone` but not `Copy`: the separator variant carries
the offending command.

### Safety

Exposure is gated by `Policy::allowed(&class, surface)`, ported from
Go's `cmdsurface/safety.go`. The default is deliberately closed: **no
remote surface may invoke a destructive leaf** —
`Policy::default_policy()` leaves `allow_destructive_on` empty, and
empty means block-all rather than an unset sentinel. A blocked call
renders as an `isError` result at HTTP 200, not a transport error: the
call was understood and declined, not malformed.

Note `Policy::default_policy()` is the named constructor `Bridge::new()`
installs; the derived `Policy::default()` leaves *both* sets empty.

Leaves are classified from `kit/*` annotations: `kit/side-effect`
(`destructive`, `destructive-local`, `destructive-shared`),
`kit/auth-required`, `kit/requires-confirmation`.

A `kit/auth-required` leaf runs only for a caller the mount's verifier
established: `MountOptions::verifier`, a `Verifier::new(|req| ...)`
returning `Option<Identity>`. An `Authorization` header alone is refused
with 401, and the refusal's `Response::www_authenticate` carries the
`Bearer` challenge for your binding to write. See [Admit authenticated callers](../guides/serve-mcp-from-any-sdk.md#admit-authenticated-callers).

### Audit

Go's bridge audits every refusal the MCP surface decides before adopter
code runs (`cmdsurface.Bridge.Audit`). This port has no sink
abstraction; `MountOptions::audit` is the one hook, an
`Arc<dyn Fn(&AuditRecord) + Send + Sync>`, called with one record per
refusal on both eras and over stdio:

```rust
use std::sync::Arc;
use hop_top_kit::mcp::{AuditRecord, MountOptions};

let options = MountOptions {
    audit: Some(Arc::new(|record: &AuditRecord| {
        eprintln!("{} refused: {} {:?}", record.surface, record.error, record.path);
    })),
    ..MountOptions::default()
};
```

`AuditRecord` is the pre-flight subset of Go's `cmdsurface.Invocation`:
`path` (the resolved leaf's segments), `surface` (`"mcp"`),
`requested_at` (when the refusal was decided), `spec_version`
(`2024-11-05` or `2026-07-28`), `error` (Go's sentinel message,
verbatim) and `extra` (`mcp_spec_version` always; the keys below). No
caller, arguments or flags: none is established or decoded when these
gates answer.

| Event | `error` | `extra` beyond `mcp_spec_version` |
|-------|---------|-----------------------------------|
| auth-required leaf, no caller established (both eras) | `cmdsurface: authentication refused` | — |
| confirmation gate unsatisfied: no `X-Confirm-Token`, or the keyed mount's no-elicitation fallback (both eras) | `mcpsdk: confirmation required` | — |
| confirmation answered decline or cancel (2026-07-28) | `mcpsdk: confirmation declined` | — |
| `requestState` failed verification: tampered, malformed, another key, binding or principal (2026-07-28) | `mcpsdk: confirmation requestState failed verification` | `mcp_confirm_rejection: request_state_verification_failed` |
| destructive leaf above the policy ceiling (both eras) | `cmdsurface: destructive command blocked on this surface` | — |

The sentinels are the `mcp::audit::ERR_*` constants. The hook is
best-effort: it runs synchronously after the refusal is decided and
before the response is returned, and a panic inside it is caught and
dropped, so it can never change the wire response. Not audited: an
unknown tool, malformed arguments, the origin allowlist, a run's own
outcome, and an expired-but-authentic `requestState` (a silent
re-prompt, as in Go).

### Long-lived mounts

The surface holds no per-request cache: `tools/list` re-reads the
bridge's leaf set every time, so a leaf set that changes after an invoke
is reflected in the next listing. That matters because Go's `Leaf` wraps
a live `*cobra.Command` and re-walks its flags per request, and cobra
attaches `--help` to a command on its first execution — two
byte-identical `tools/list` requests on one mount therefore differ
across an intervening `tools/call`. See
`Leaf::with_flags_on_first_execution`, and the `sequences` section of
the wire fixtures, which pins it.

### Scope

Deprecated upstream features (Roots, Sampling, Logging, HTTP+SSE) are
unimplemented, matching the Go reference. No `tasks/*` method is
served: each answers `-32601`. `tasks_enabled` only declares the
extension in `server/discover`; left off (the default), no
`extensions` map is advertised, which is the conformant way to not
support it.

### Cross-references

- [Serve MCP from any SDK](../guides/serve-mcp-from-any-sdk.md)
  — the polyglot adopter guide
- [Expose your CLI over MCP](../guides/expose-cli-over-mcp.md)
  — the Go reference surface, in depth

## Identity

Feature `identity` ports `go/core/identity`: an Ed25519 `Keypair` (PKCS#8/SPKI PEM byte-identical to
Go's), EdDSA JWT sign and verify with Go's claims checks, `derive_key` (HKDF-SHA256) and NaCl
secretbox `encrypt`/`decrypt`, and a filesystem `Store`. The key-derivation bytes are pinned by
`contracts/identity-v1` (`make test-parity-identity`).

The core builds for `wasm32-unknown-unknown`, which has no OS random source and no clock: there,
build keys with `Keypair::from_seed`, seal with `encrypt_with_nonce` and verify with
`verify_jwt_at` / `verify_jwt_with_options_at` (unix seconds). `Keypair::generate`, `encrypt`,
`verify_jwt` and `verify_jwt_with_options` are compiled off that target; `Store` and
`default_store` (`<data home>/kit/identity`) off wasm32 altogether. See
[`src/identity/README.md`](../../../sdk/experimental/rs/src/identity/README.md) for the contract and
the differences from Go.

## Storage

Four storage modules port the cores of the Go `go/storage/*` packages.
They stack: `sqldb` is the connection primitive, `kv` and `sqlstore` build
on it, and `httpcache` builds on `kv`.

```toml
[dependencies]
hop-top-kit = { version = "0.5.0-alpha.6", features = ["kv"] } # x-release-please-version
```

```bash
cargo build --features kv
cargo test  --features kv
```

### kv keys must bind as TEXT

The single most important thing to know about `kv`: keys go into SQLite as
**TEXT**, and that is not a stylistic choice.

SQLite treats TEXT and BLOB as distinct storage classes and compares
storage class before it compares value. A key bound as a BLOB therefore
never equals the same bytes bound as TEXT. Get the binding wrong and
nothing raises an error — reads across languages become silent misses,
`INSERT OR REPLACE` writes a shadow row beside the one it was meant to
replace instead of replacing it, and prefix range scans return disjoint
sets.

Cross-language access to one SQLite file is a hard requirement here, not a
hypothetical, so the binding is part of the contract. Two consequences
follow that are easy to get wrong:

- **The column declaration is not the contract; the bind type is.** Both
  languages create their table with `CREATE TABLE IF NOT EXISTS`, so
  whichever process opens the file first wins and the other language's
  declaration is inert. Declaring the column `TEXT` proves nothing about
  what a peer actually binds.
- **Keys are `[u8]`, not `String`.** Go models keys as `string`, which is
  an arbitrary byte sequence, and the shared corpus includes prefixes such
  as `data\xff` that are not valid UTF-8. Rust's `String` cannot hold
  those, so the API takes bytes and binds them as TEXT without UTF-8
  validation. `put_str` and friends cover the common UTF-8 case.

TEXT also preserves the ordering Go relies on: the default `BINARY`
collation is `memcmp` over stored bytes, which matches Go string
comparison, so prefix scans agree in both languages even for non-UTF-8
keys.

### Why a cross-process gate exists

Neither language's own test suite can catch a binding mismatch, by
construction: the Rust suite round-trips Rust to Rust and the Go suite Go
to Go, so both sides pass while agreeing only with themselves. The gate
that actually crosses the boundary is driven from the shared corpus in
[`contracts/kv-v1/keys.json`](../../../contracts/kv-v1/keys.json): the Go
test in `go/storage/kv/sqlite/crosslang_test.go` invokes the Rust harness
in `tests/kv_crosslang.rs` as a subprocess, so one language writes the
database and the other reads it.

Because it needs both toolchains present it lives in the parity job rather
than in either language's own suite:

```bash
make test-parity-kv
```

The harness entry points key off the `KV_CROSSLANG_DB` environment
variable and skip without it, so a plain `cargo test --features kv` stays
green on a machine with no Go toolchain. The remaining tests in that file
are Rust-only and always run.

### blob writes are atomic

`blob::local::LocalStore::put` stages contents in a sibling temp file,
syncs it, then renames it over the destination. A concurrent `get` never
observes a partial blob, and an interrupted write leaves any previous
value intact. The Go local backend matches this. Only the local backend is
ported; see [Not ported](#not-ported).

### sqlstore migrations differ from Go

Go's `sqlstore` runs its own `CREATE TABLE IF NOT EXISTS` and concatenates
`Options.MigrateSQL` onto it on every open. This port routes everything
through `sqldb::migrate` instead: the store owns version 1 for its `kv`
table and caller SQL lands at version 1000, leaving the range between free
for future built-in migrations.

That buys three things Go's path lacks — caller SQL runs in a transaction
and rolls back on failure, runs exactly once rather than on every open (so
non-idempotent seed inserts are safe), and is introspectable through the
same `schema_versions` table every other `sqldb` consumer uses.

One behavioural difference follows: because caller SQL is versioned,
editing `Options::migrate_sql` after the database exists will not re-run
it. That is correct migration semantics, but it does differ from Go's
run-every-open treatment.

### At-rest encryption

`sqlstore-encrypt` adds `EncryptedStore`, which derives a symmetric key
with HKDF-SHA256 from an Ed25519 private key seed and encrypts values with
NaCl secretbox (XSalsa20-Poly1305, random 24-byte nonce). Only the 32-byte
seed feeds the KDF, never the expanded private key.

Key derivation is deterministic, which makes it a wire contract in its own
right — a store encrypted by Go must be readable here and vice versa. The
shared vectors live in
[`contracts/identity-v1/derive-key.json`](../../../contracts/identity-v1/derive-key.json)
and are asserted from `tests/sqlstore.rs`.

### Cross-references

- Go canonical implementations live under
  [`go/storage/`](../../../go/storage/) and
  [`go/core/identity/`](../../../go/core/identity/).

## httpcache wire contract

Where Go decorates an `http.RoundTripper`, this port models the exchange as
plain data and takes the fetch as a closure, so it pulls in neither an HTTP
client nor an async runtime. Callers wire it to whatever transport they
already use.

The JSON fixtures in [`contracts/httpcache-v1/`](../../../contracts/httpcache-v1/)
are the **contract of record** for `httpcache`, not the Go source. Go is
currently the only implementation, so a second port does not conform to a
settled format — it pins the format for every later one. Read the fixtures
first and treat any disagreement with the Go source as a bug in whichever
side the fixtures do not cover.

| Fixture | Pins |
|---------|------|
| `keying.json` | `prefix + sha256(method + " " + url)`, plus which URL transforms are and are not applied |
| `entry.json` | The on-store envelope: `status`, `headers`, `body`, and the framing-header strip/recompute rule |
| `cacheability.json` | Which requests and responses may be stored |

Three details diverge from what a serializer's defaults would produce:

- **`body` is a base64 string, not an array of byte numbers.** Serde renders
  `Vec<u8>` as `[104,105]` by default; the contract requires `"aGk="`.
  Encode and decode explicitly at the field boundary.
- **Standard base64 alphabet, with padding.** Not URL-safe, not unpadded.
  A URL-safe decoder rejects conforming envelopes and emits unreadable ones.
- **`headers` maps to a *list* of strings**, so duplicate `Set-Cookie` and
  `Vary` values survive. A map to a bare string is lossy and non-conforming.

Keying takes the method and URL **verbatim** — no method upper-casing, host
lower-casing, default-port stripping, query sorting, dot-segment resolution,
or fragment stripping. A URL type that normalizes on re-serialization will
silently produce keys Go cannot read; `keying.json` has a case for each.

`serde` + `serde_json` + `base64` + `sha2` cover the whole contract; nothing
exotic is needed.

The fetch closure owns the transport, so `--offline` is enforced only if
the closure's client is a [`GuardedClient`](#offline-enforcement).

## Offline enforcement

The family-wide `--offline` global disables network access. It is the
highest-precedence override: per-command network opt-ins behave as if
their opt-out flag had been passed. It only forces opt-outs on — it never
un-sets an explicitly passed `--no-*` flag.

Loopback is exempt. `--offline` means "do not talk to the network", not
"do not talk to myself", so `127.0.0.0/8`, `localhost` (any case) and
`[::1]` stay reachable. Other hostnames are remote even when they would
resolve to loopback, because performing that resolution is itself
network access.

Feature `api` carries it (`telemetry` enables `api`).

### Setting the policy

```rust
use hop_top_kit::netpolicy::NetPolicy;

let policy = NetPolicy::new(offline); // from your parsed --offline flag
policy.is_offline();                  // consult
```

Go threads the marker through `context.Context`; Rust has no ambient
request context, so the marker is a `NetPolicy` value handed to each
client at construction. `NetPolicy::default()` is online, so a client
built without one behaves as before.

### Enforcement

The policy alone is advisory — a caller who forgets to consult it still
reaches the wire. Rust also has no process-wide default transport to
install a guard over, so the chokepoint is construction: `GuardedClient`
is the crate's only `reqwest::Client` construction path, it never hands
out the inner client, and every request it sends passes the policy check.

```rust
use std::time::Duration;
use hop_top_kit::netpolicy::{GuardedClient, NetPolicy};

let client = GuardedClient::new(NetPolicy::new(offline))?;
// or, with transport settings:
let client = GuardedClient::build(
    NetPolicy::new(offline),
    reqwest::Client::builder().timeout(Duration::from_secs(10)),
)?;

let resp = client
    .get("https://api.example.com/v1/items")
    .bearer_auth(&token)
    .send()
    .await?;
```

The request builder offers `header`, `bearer_auth`, `body`, `json` and
`query`; for anything else, build a `reqwest::Request` and pass it to
`client.execute(req)`, which applies the same check.

`ApiClient` is built on `GuardedClient`, so its methods are enforced
with no opt-in. `ApiClient::new(url)` is online; pass the flag through
`ApiClient::with_policy(url, NetPolicy::new(offline))`.

### Detecting a refusal

A blocked request returns an error rather than skipping silently:

```text
GET https://api.example.com:8443/v1/models: network disabled by --offline
```

The message names the method and the destination as scheme, host, port
and path only; query, fragment and userinfo never appear, as they may
carry credentials. See
[Offline refusals](../guides/cli-parity-guide.md#offline-refusals) for
the shape every port shares.

Match it by type. `GuardedClient` returns `NetError`, whose
`as_offline()` yields the `OfflineError` (`method`, `url`) and whose
`is_offline()` is the boolean form:

```rust
match client.get(url).send().await {
    Ok(resp) => { /* … */ }
    Err(e) if e.is_offline() => { /* refused by --offline: degrade, don't retry */ }
    Err(e) => return Err(e.into()),
}
```

Behind a `Box<dyn Error>`, downcast first:
`err.downcast_ref::<NetError>().and_then(NetError::as_offline)`.
`ApiClient` maps a refusal to `ApiError` with `status` 0 and `code`
equal to `hop_top_kit::api::OFFLINE_CODE` (`"offline"`); an attempted
request that failed gets `code` `"transport_error"`.

### What other errors carry

Every error `GuardedClient` returns names its destination the same way:
`NetError::Transport` (connect, send, or a request that failed to
build) has its URL replaced by scheme, host, port and path, so its
`Display`, `Debug` and `url()` never carry query, fragment or userinfo.
`ApiClient` errors follow the same rule, its body decoding included.

A `reqwest::Response` that `GuardedClient` hands back is reqwest's own:
its `url()`, and the errors from its `json()`, `text()` and `bytes()`,
still carry the full URL. Strip it before you print or log one —
`err.without_url()` drops it — and keep credentials in headers, not
URLs.

### Scope

Enforcement covers HTTP(S) sent through `GuardedClient`, and so
`ApiClient`: every network client in this crate except the telemetry
sink below. It does
**not** cover a `reqwest::Client` you build yourself, raw `TcpStream`,
gRPC, raw TLS or a networked database driver. For those `--offline` stays
advisory and the call site must consult `NetPolicy::is_offline()`
itself.

The telemetry HTTPS sink is deliberately **not** guarded: it builds its
own `reqwest::Client`. Telemetry is logging-class egress: `--offline`
stops traffic the user asked for, it is not a second consent gate on
diagnostics. Consent and telemetry mode already govern whether anything
is emitted.

### CLI flag

Not yet wired. This crate's `cli` module is an empty placeholder, so
nothing registers `--offline` for you. Declare it on your clap root and
pass `NetPolicy::new(offline)` to every `GuardedClient` and
`ApiClient::with_policy` you build.

## Telemetry

The Rust telemetry SDK is feature-gated. It mirrors the Go canonical
runtime (`hops/main/go/runtime/telemetry`) at the data-only seams and
is **default-denied**: nothing is emitted until both
`KIT_TELEMETRY_MODE` (or `<APP>_TELEMETRY_MODE`) AND the persisted
consent file say so.

```toml
[dependencies]
hop-top-kit = { version = "0.5.0-alpha.6", features = ["telemetry"] } # x-release-please-version
```

```bash
cargo build --features telemetry
cargo test  --features telemetry
```

### Runtime requirement

The client uses `tokio::sync::mpsc` and `tokio::spawn` for its
background drain. **A tokio runtime MUST be live at `Client::new`**.
Adopters without an existing runtime should wrap construction in a
`tokio::runtime::Builder::new_current_thread().enable_all().build()`
context until the planned `tokio-current-thread` helper ships.

### Quick start

```rust
use hop_top_kit::telemetry::{Client, ClientOptions, SinkKind};
use serde_json::json;

#[tokio::main]
async fn main() {
    let client = Client::new(ClientOptions {
        sink: SinkKind::Jsonl,
        sink_file: Some("/tmp/kit-events.jsonl".into()),
        ..Default::default()
    })
    .expect("telemetry client");

    // record() is fire-and-forget. Never blocks. Returns Ok(()) even
    // when the queue is full (dropped count surfaces via
    // client.dropped_count()).
    client.record("app.start", json!({"version": "1.0"})).unwrap();
}
```

### Custom redactor escape hatch

The default redactor (`telemetry::redact`) is best-effort: emails,
IPv4/IPv6, `$HOME` paths, and a handful of bearer-token prefixes
(`sk-`, `ghp_`/`gho_`/`ghu_`/`ghs_`/`ghr_`, `xoxb-`). Adopters with
stricter PII policies pass a custom redactor that runs **before** the
default one (defense in depth):

```rust
use hop_top_kit::telemetry::{Client, ClientOptions, SinkKind};
use serde_json::Value;

let redactor = Box::new(|v: Value| -> Value {
    // strip everything except a fixed allowlist
    serde_json::json!({})
});

let _ = Client::new(ClientOptions {
    sink: SinkKind::Jsonl,
    sink_file: Some("/tmp/kit-events.jsonl".into()),
    redactor: Some(redactor),
    ..Default::default()
});
```

Compliance-sensitive adopters should route SDK events through a Go-side
collector that re-emits via `go/core/redact`.

### Sinks

- `SinkKind::Jsonl` — append-only `.jsonl` file with 10 MB rotation.
  Default; pairs well with a Go-side collector that tails the spool.
- `SinkKind::Https` — POST `application/x-ndjson` to `endpoint`, 5s
  connect / 10s overall timeout, one retry on 5xx / transport. Not
  refused by `--offline`; see [Offline enforcement](#offline-enforcement).

### Env vars

| Env var | Purpose |
|---------|---------|
| `KIT_TELEMETRY_MODE` | `off \| anon \| full` (SDK-level). |
| `<APP>_TELEMETRY_MODE` | Overrides KIT-level when `KIT_APP_PREFIX` is set. |
| `KIT_TELEMETRY_ENDPOINT` | HTTPS sink target (used by `ClientOptions::from_env`). |
| `KIT_TELEMETRY_SINK` | `https \| jsonl` (used by `ClientOptions::from_env`). |
| `KIT_TELEMETRY_SINK_FILE` | JSONL path (used by `ClientOptions::from_env`). |
| `KIT_TELEMETRY_QUEUE_SIZE` | Bounded channel capacity, defaults to 1024. |
| `XDG_CONFIG_HOME` | Locates the consent file (`<HERE>/kit/config.yaml` at `kit.telemetry.consent`; legacy `<HERE>/kit/telemetry.yaml` read as fallback). |
| `XDG_STATE_HOME` | Locates the install_id file (`<HERE>/kit/telemetry/installation_id`). |

### Cross-references

- Schema doc: [`sdk/docs/telemetry-event-schema.md`](../../../sdk/docs/telemetry-event-schema.md).
- Go canonical implementation:
  [`go/runtime/telemetry/README.md`](../../../go/runtime/telemetry/README.md).

## Bus

In-process event bus, feature-gated. Ports the core of the Go canonical
runtime (`go/runtime/bus`): the `Topic` type and its wildcard matching,
both topic validators, the `Event` envelope, `Qualifiers`, and in-memory
publish/subscribe dispatch.

```toml
[dependencies]
hop-top-kit = { version = "0.5.0-alpha.6", features = ["bus"] } # x-release-please-version
```

```bash
cargo build --features bus
cargo test  --features bus
```

Adds no dependencies beyond `serde` and `serde_json`, which the SDK
already carries.

### Topic notation

Topics follow `[Source].[Category].[Object].[Action]`, with a past-tense
action segment — `crm.sales.deal.created`, `billing.finance.invoice.paid`.

Two validators exist, deliberately, with different strictness:

| Function | Contract | Used by |
|----------|----------|---------|
| `validate` | 4 segments matching `^[a-z][a-z0-9_]*$`, total length <= 128, wildcards rejected. Does not check verb tense. | `publish`, per the configured `Mode`. |
| `validate_topic` | Additionally requires a past-tense action segment (ends in `ed`, or appears in `PAST_TENSE_WHITELIST`). | `prefix_topics`, so misconfigured topic maps fail during adopter wiring. |

Subscribe patterns keep wildcard support: `*` matches exactly one
segment, `#` matches zero or more trailing segments and must come last.

### Quick start

```rust
use hop_top_kit::bus::{DynBus, Event, Mode};
use serde_json::json;

let bus = DynBus::builder().enforce(Mode::Strict).build();
bus.subscribe("crm.sales.deal.*", |e| {
    println!("{} from {}", e.topic, e.source);
    Ok(())
});

let event = Event::new("crm.sales.deal.created", "crm", json!({"id": 1}));
bus.publish(&event).unwrap();
bus.close().unwrap();
```

`DynBus` is the front door, the counterpart of Go's `bus.New`. Every
method takes `&self`, and clones share one bus, so handlers can be
registered in one place and events published in another. `DynBus::new()`
is `New()` with no options. `Bus` and `SharedBus` remain as the
in-memory building block underneath, with no adapter, network or sink
layers.

### Adapter seam

`Adapter` is the transport trait, Go's `Adapter` interface:
`publish`, `subscribe`, `subscribe_async`, `unsubscribe` and `close`.
`MemoryAdapter` is the in-process implementation, as Go's
`NewMemoryAdapter`. `DynBus` implements `Adapter` too.

```rust
use hop_top_kit::bus::{DynBus, MemoryAdapter};

let bus = DynBus::builder().with_adapter(MemoryAdapter::new()).build();
```

`build()` stacks the layers in Go's `New` order: the adapter behind
topic enforcement (or, with none, the in-memory bus), then the network
layer, then the env sinks. With an adapter, `max_async` does not apply,
as in Go.

`with_network(addrs)` is Go's `WithNetwork` (feature `bus-network`):
`build()` dials each address once and keeps retrying a failed one in
the background; `network_options(...)` is Go's `WithNetworkOption`.
Without the feature it panics on any address, so a bus can't silently
stay local; an empty list is a no-op.

### Dispatch

Sync handlers run inline on the publisher's thread, in subscription
order, and the first handler error vetoes the publish. Async handlers
(`subscribe_async`, Go's `SubscribeAsync`) run after every sync handler
succeeds, on a `Spawn` you supply for your executor, from a pool sized
by `max_async` (default 256). `close_ctx` drains them until its context
is done. No async runtime is pulled in; see
[rust-bus-parity.md](../../contributors/architecture/rust-bus-parity.md).

### Network

`bus::network::NetworkAdapter` is Go's `NetworkAdapter` over a native
WebSocket: local events go to linked peers, peer events are published
locally, and origin ids stop loops. Go and Rust peers mix in either
role.

| Feature | Adds |
|---------|------|
| `bus-network` | the adapter: `bus` + `netpolicy`, `tokio` (rt, net, time, sync, macros), `tokio-tungstenite`, `futures-util`, `getrandom`, `rustls` (ring provider), `rustls-native-certs` |
| `bus-network-axum` | `NetworkAdapter::axum_handler()`: `axum`, `hyper`, `hyper-util` |
| `bus-network-webpki-roots` | Mozilla's roots (`webpki-roots`) added to the default TLS roots |

```rust
use hop_top_kit::bus::network::{NetworkAdapter, NetworkOptions};
use hop_top_kit::bus::{Context, DynBus};

let bus = DynBus::new();
let net = NetworkAdapter::new(bus.clone(), NetworkOptions::new().relay(true));
let addr = net.listen("127.0.0.1:0")?;              // serve peers
net.connect(&Context::background(), "ws://hub:8080/bus")?; // or dial one
```

- **Runtime.** The adapter's I/O runs on one shared current-thread tokio
  runtime, on its own `kit-bus-network` thread; callers need no runtime.
  `connect`, `disconnect`, `close` and `listen` block the calling thread
  and panic when called on that runtime; from async code use
  `connect_async`, `disconnect_async` and `close_async`. Bus handlers
  run for inbound events on tokio's blocking pool. `TokioSpawn` is a
  `Spawn` for `subscribe_async` on your own tokio runtime.
- **TLS.** `wss://` uses rustls on the ring provider with the
  platform's roots, as Go's default transport does. Android has no root
  store rustls can read: pass `NetworkOptions::tls_config(config)` with
  an `Arc<rustls::ClientConfig>` (from `rustls-platform-verifier`, or
  your CA in a `RootCertStore`), or enable `bus-network-webpki-roots`.
  `ring` compiles C for the target (Xcode for iOS, the NDK for Android).
- **Events.** `kit.runtime.link.connected`, `kit.runtime.link.disconnected`
  and `kit.runtime.stream.invalidated` are published locally as Go
  publishes them; `NetworkOptions::error_func` gets failed handshakes.
- Accepting: `listen`, `accept_upgraded` after `check_upgrade`, or the
  axum handler. A second inbound link under an address already linked
  is refused with 1000 `duplicate`; behind a proxy, key links by the
  client's own address.
- Not yet ported: stream semantics (remote subscribe, resume, outbox),
  CloudEvents outbound frames, and the browser transport.

### Not ported

The Go package's SQLite bus adapter is deliberately absent, as are kv's
etcd/TiDB backends and blob's S3 backend. See
[rust-bus-parity.md](../../contributors/architecture/rust-bus-parity.md).

### Cross-references

- Go canonical implementation:
  [`go/runtime/bus/README.md`](../../../go/runtime/bus/README.md).

## Config

Layered YAML configuration, feature-gated. Ports Go `go/core/config`:
`load` merges defaults, system, user and project files, extra `-c`
paths, an env override hook and `-c key=value` overrides into a typed
serde struct with yaml.v3's merge rules; `get`, `list`, `set`,
`set_value` and `unset` read and write single keys; `Reloadable` hot
reloads with an immutability veto and bus events.

```toml
[dependencies]
hop-top-kit = { version = "0.5.0-alpha.6", features = ["config"] } # x-release-please-version
```

| Feature | Adds |
|---------|------|
| `config` | the module (builds on `bus`); YAML via `saphyr-parser` |
| `config-signal` | `Reloadable::watch_signal` on tokio Unix signals |
| `config-pkl` | PKL schema parsing, validation, completion, `resolve` via the `pkl` binary |

Merge, precedence, scalar typing, overrides and edits are pinned by the
[`config-v1`](../../../contracts/config-v1/README.md) corpus
(`make test-parity-config`). The pure parts build on wasm32; files,
paths, writes and reload do not.

### Differences from Go

- Writes splice only the targeted entry: values, kept comments and key
  order match Go, bytes do not (Go re-emits the document). An edit that
  cannot keep every comment and anchor outside the entry is refused
  with `ErrorKind::EditUnsupported`, file untouched.
- No viper: `EnvLookup` (`bind_env`, `seed_defaults`, `sync`,
  `Options::env_lookup`) keeps viper's lookup order and implements the
  bus's `ConfigGetter`, as does a loaded `Value`.
- Reload mutability comes from the `ReloadFields` trait, not
  `reload:"true"` tags; pin each list with `check_reload_fields` in a
  test. Config structs derive `Default` with `#[serde(default)]`.
- `watch_signal` is called on an `Arc<Reloadable<T>>` and reloads on
  tokio's blocking pool.
- Timestamps stay text; decode errors name Rust types.
- Not ported yet: the PKL wizard, `RunWizard` and the config command
  (they need a console wizard and a CLI root), and the rest of
  `go/core/xdg` beyond home and config home.

The full list is in the `hop_top_kit::config` module docs.

## Notify

Feature `notify` ports `go/runtime/notify`; the module README
([`src/notify/README.md`](../../../sdk/experimental/rs/src/notify/README.md))
carries the contract, the SMTP details are in
[`src/notify/email/README.md`](../../../sdk/experimental/rs/src/notify/email/README.md)
and the Go-side option tables are in [notify sinks](notify-sinks.md). The
Rust-specific behaviour:

- Features: `notify` is the wasm-safe core (`bus` and nothing else):
  `Severity`, `severity_of`, `FilterSink`, `RetrySink`,
  `exponential_backoff`, `Template`, `TextTemplate`, `LiteralTemplate`.
  The sinks are native only, one feature each: `notify-webhook` (`api`,
  `breaker`, `redact`, tokio), `notify-email` (`breaker`, `redact`,
  `netpolicy`, `base64`, `rustls`, `rustls-native-certs`), `notify-os`
  (`breaker`, `redact`).
- Severity is read from a JSON object payload's `severity` member: a
  lowercase keyword or a number `0..=4` (a float must have no fraction);
  anything else is `Info`. There is no typed `WithSeverity`: a typed
  payload advertises severity by serializing the key. Go's
  `Severity(99).String()` = `unknown` has no counterpart; the enum has
  five values.
- Retry counts as Go does: attempt 0 is the first try, the wait before
  attempt `n` is `backoff(n - 1)`, `max_attempts` below 1 is 1, a context
  that ends during a wait returns `BusError::Context`. On exhaustion: the
  dead-letter sink's result, else the last inner error as it came.
- A breaker refusal is terminal for `RetrySink`. A `Sink` reports errors
  as `BusError::Handler(message)`, so the refusal is recognized by its
  text, `BROKEN_CIRCUIT`, anywhere in the message (`is_broken_circuit`);
  the sinks' own wrapping ("webhook: post: ...") still matches. A sink of
  yours that gates on a breaker reports
  `BusError::handler(BrokenCircuit.to_string())`.
- The default retry wait parks the thread and wakes early on cancellation
  or the deadline; it is not compiled for wasm32, where it does not wait:
  give `RetrySink::sleeper` one.
- Templates are Go's `text/template` reduced to literal text and
  `{{.Field}}` actions (`.Topic`, `.Source`, `.Timestamp`, `.ID`,
  `.WorkspaceID`, `.Subject`, `.DataContentType`, `.DataSchema`,
  `.Payload` and `.Payload.a.b` into a JSON object). Bad syntax fails at
  construction, an unknown field at render. A string renders raw, `null`
  as `<nil>`, a missing member as `<no value>`, any other value as
  compact JSON.
- Webhook: POST through the guarded `api` client, so an offline
  `NetPolicy` (`.policy(..)`) refuses a remote endpoint and allows
  loopback. A non-2xx is an error carrying the status and at most 512
  bytes of the body. Default timeout 5s; `.header` accumulates and
  repeats; `.auth_bearer`. Redirects are not followed (Go follows up to
  ten): a 3xx is a non-2xx. `drain` is synchronous: it runs the request
  on a current-thread runtime on a helper thread, so it is safe inside an
  async task, and it honors the `Context`'s cancellation and deadline.
- OS: macOS `osascript -e 'on run argv' ... -- <text> <title>` (title and
  text are the script's argv, never interpolated into its source), Linux
  `notify-send -- <title> <text>` (probed on `PATH` in
  `OsNotifySink::new`), Windows an error. `--` keeps a leading `-` from
  being read as an option. Spawned directly, never through a shell;
  `Runner` is the test seam (`.runner(..)`, `for_platform(..)`).
  `ExecRunner` kills the child when the context ends.
- Not ported: the typed-payload `WithSeverity` path. No `contracts/`
  corpus covers notify; the Go tests are ported (see `tests/notify_*.rs`).

## Policy

Feature `policy` ports `go/runtime/policy`; the module README
([`src/policy/README.md`](../../../sdk/experimental/rs/src/policy/README.md))
carries the rule and activation contract.

### Differences from Go

- CEL is `cel-rust`, not `cel-go`: no static type check. A typo'd
  identifier fails at evaluation and denies; `==` across types is false,
  so a mistyped rule takes its `otherwise`. `timestamp()` and
  `duration()` are not compiled in (the `chrono` feature needs
  `chrono-tz`); a `stage.since` is a string. Everything the shipped rules
  and Go's tests use (`has`, `in`, `&&`, `||`, `!=`, `matches`, `size`)
  works.
- No `ErrConflict` sentinel: `PolicyDeniedError::is_conflict()`.
- Go's `withcel.New(cfg, opts...)` is `EngineBuilder::cel()` then
  `build(&cfg)`.
- `Engine::decide` takes the activation by value; Go mutates the
  caller's map.
- No contract directory: Go has no shared policy vectors. The Go tests
  are ported in `tests/policy*.rs`, reading Go's rule files in place;
  `stage.yaml` is embedded and a test pins it byte-for-byte to Go's.

### Dependency vetting

`cel =0.15.0` (cel-rust/cel-rust), default features off, `regex` on.

- Alternatives: `cel-interpreter` 0.10.0 is the same project's older name,
  last released 2025-07-23 and superseded by `cel`; no other maintained
  CEL implementation exists.
- Maintenance: pushed 2026-10-23, 35 contributors, 8 releases since
  2026-03, not archived. `rsx analyze` scores 6.2/10 and passes the
  balanced policy (maintenance 10, license 10; adoption and security
  scores are low for registry reach, not for findings).
- Licence: MIT; the tree adds `antlr4rust` (BSD-3-Clause), `nom`,
  `winnow`, `typed-arena` (MIT), and MIT/Apache crates (`bit-set`,
  `bit-vec`, `borsh`, `byteorder`, `murmur3`, `pastey`, `minimal-lexical`,
  `proc-macro-crate`, `toml_edit`). All permissive, compatible with the
  crate's `MIT AND Apache-2.0`.
- Advisories: `cargo deny check advisories` clean on the resolved tree.
- Age: 0.15.0 published 2026-10-06, 25 days before the pin. Exact-pinned
  because rule semantics are the policy contract.
- wasm: `cargo build --target wasm32-unknown-unknown --features policy`
  builds; the smoke test runs under Node. 48 normal crates in the tree.

## Crate-wide contract

- `serve <service>` runs a named service even when disabled; a `serve` the clap root already owned is replaced, never doubled.
- `output` ships `table`, `json` and `yaml` only (no `csv`, `text`); column order comes from an explicit `ColumnSpec` list; `--cols` reorders as well as selects; `header` must equal `key`.
- MCP exposure is default-closed: the default policy blocks every destructive leaf on every remote surface. Telemetry is default-denied: nothing emits without both a granted consent decision and a non-`off` mode. Consent grants fail closed: no prompter, an unreadable store or a failed audit is a refusal.

## Authentication

Feature `authn` ports `go/transport/authn`: an own JWS verifier, key
sets, claim checks and the kit-owned `Claims` / `Identity` vocabulary,
wasm-safe and free of any server. `authn-rsa` adds RS256/384/512 and
PS256/384/512 over `ring` (native); `authn-apikeys` adds the kv-backed
opaque keys. The module README
([`src/authn/README.md`](../../../sdk/experimental/rs/src/authn/README.md))
carries the contract; the points that differ from Go:

- No JWT crate: the signature primitives are RustCrypto (`p256`, `p384`,
  `p521`, `ed25519-dalek` with `verify_strict`) behind `authn/provider/`,
  and the public types are kit's (`VerifyKey`, `Key`, `Jwk`, `Verifier`).
  RSA is `ring` rather than the `rsa` crate, which has no release fixed
  for RUSTSEC-2023-0071.
- A transport hands the verifier header values (`Credentials`), not an
  HTTP request, so `AuthFunc` is a plain closure and the module compiles
  without an HTTP crate.
- Time is `Options::now`; on wasm32 it is required.
- The token and API-key corpus in
  [`contracts/authn-v1`](../../../contracts/authn-v1/README.md) runs in
  both ports (`make test-parity-authn`).
- Not ported: fetching and caching a remote JWKS or OIDC discovery
  document. Back a `KeySource` with a guarded fetch.


## Build notes

Counts in the front page's feature table are non-dev crates, root
excluded, from `cargo tree --no-default-features --features <feature> -e
normal`. `api` and `telemetry` are heaviest (`reqwest` brings `tokio` and
TLS); bundled `rusqlite` builds SQLite from source (needs a C compiler).

- The `notify` sinks are native only: `notify-webhook` goes through the
  guarded `api` client and runs on a current-thread tokio runtime on a
  helper thread; `notify-email` speaks SMTP itself over a policy-checked
  dial and needs `ring`'s C compiler for STARTTLS; `notify-os` shells out
  to `osascript` or `notify-send`.
- The `llm` HTTP adapters are native only and retry on tokio's timer
  unless given a `Sleeper`.
- `bus-network` runs one shared current-thread tokio runtime on its own
  thread, where its blocking methods panic (use the `_async` variants);
  `ring` needs a C compiler for the target; `wss://` on Android needs
  `NetworkOptions::tls_config` or `bus-network-webpki-roots`.

<!-- release: track hop-top-cite 0.1.0 -->
