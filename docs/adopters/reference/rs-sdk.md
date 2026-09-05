# Rust SDK Reference

> Every long-form surface of the experimental `hop-top-kit` crate
> (`sdk/experimental/rs`): serve, the URI facade, output formatting
> rules, the MCP mount, storage, the httpcache wire contract, telemetry
> and the bus. The crate front page is
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
hop-top-kit = { version = "0.1", features = ["uri"] }
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
hop-top-kit = { version = "0.5.0-alpha.0", features = ["mcp"] }
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

### Options

`MountOptions` is a plain struct with `Default`, not a builder: `path`
(`/mcp`), `server_name` (`cmdsurface`), `server_version` (`0.0.0`),
`spec_versions` (`None` = both eras), `cache_ttl_ms` (`0`),
`cache_scope` (`Private`), `origin_allowlist` (empty, no check),
`confirmation_key` (`None`), `tasks_enabled` (`false`). The option *set*
is normative across every kit SDK; only the spelling is idiomatic.

`Surface::mount` returns `Result<Surface, MountError>` rather than
starting a mount that quietly serves nothing:

| Variant | Trigger |
|---------|---------|
| `NoSpecVersions` | `spec_versions: Some(vec![])` |
| `NegativeCacheTtl` | `cache_ttl_ms < 0` |
| `EmptyConfirmationKey` | `confirmation_key: Some(vec![])` |

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
unimplemented, matching the Go reference. The `tasks/*` extension is
opt-in via `tasks_enabled`; left off it answers `-32601` with no
`extensions` map advertised in `server/discover`, which is the
conformant way to not support it.

### Cross-references

- [Serve MCP from any SDK](../guides/serve-mcp-from-any-sdk.md)
  — the polyglot adopter guide
- [Expose your CLI over MCP](../guides/expose-cli-over-mcp.md)
  — the Go reference surface, in depth

## Storage

Four storage modules port the cores of the Go `go/storage/*` packages.
They stack: `sqldb` is the connection primitive, `kv` and `sqlstore` build
on it, and `httpcache` builds on `kv`.

```toml
[dependencies]
hop-top-kit = { version = "0.5.0-alpha.0", features = ["kv"] }
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

## Telemetry

The Rust telemetry SDK is feature-gated. It mirrors the Go canonical
runtime (`hops/main/go/runtime/telemetry`) at the data-only seams and
is **default-denied**: nothing is emitted until both
`KIT_TELEMETRY_MODE` (or `<APP>_TELEMETRY_MODE`) AND the persisted
consent file say so.

```toml
[dependencies]
hop-top-kit = { version = "0.4.0-experimental.2", features = ["telemetry"] }
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
  connect / 10s overall timeout, one retry on 5xx / transport.

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
hop-top-kit = { version = "0.5.0-alpha.0", features = ["bus"] }
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
| `validate` | 4 segments matching `^[a-z][a-z0-9_]*$`, total length <= 128, wildcards rejected. Does not check verb tense. | `Bus::publish`, per the configured `Mode`. |
| `validate_topic` | Additionally requires a past-tense action segment (ends in `ed`, or appears in `PAST_TENSE_WHITELIST`). | `prefix_topics`, so misconfigured topic maps fail during adopter wiring. |

Subscribe patterns keep wildcard support: `*` matches exactly one
segment, `#` matches zero or more trailing segments and must come last.

### Quick start

```rust
use hop_top_kit::bus::{Bus, Event, Mode};
use serde_json::json;

let mut bus = Bus::builder().enforce(Mode::Strict).build();
bus.subscribe("crm.sales.deal.*", |e| {
    println!("{} from {}", e.topic, e.source);
    Ok(())
});

let event = Event::new("crm.sales.deal.created", "crm", json!({"id": 1}));
bus.publish(&event).unwrap();
```

`Bus::subscribe` needs `&mut self` while `publish` needs `&self`. When
handlers are registered in one place and events published in another,
wrap the bus in `SharedBus`, which hands out cheap clones over a single
`RefCell`.

### Dispatch is synchronous

Delivery runs inline on the publisher's thread, in subscription order,
and the first handler error vetoes the publish. The Go package's
async-handler path — goroutine pool, bounding semaphore, `WaitGroup`
drained by `Close` under a deadline — is **not** ported. No Rust consumer
needs concurrent delivery, and a faithful port would make an async
runtime a hard dependency of this feature. See the `bus::mem` module
docs for the decision in full.

Consequence: a slow handler blocks the publisher, and `publish` returns
only once every matching subscriber has run. Handlers that want to defer
work should spawn a task themselves.

### Not ported

The Go package's `NetworkAdapter` (WebSocket peer relay, reconnect and
backoff, star topology, auth handshake) and its SQLite adapter are
deliberately absent, as are kv's etcd/TiDB backends and blob's S3
backend. See ADR 0040 for the rationale and for what to build should
cross-process eventing become a real requirement.

### Cross-references

- Go canonical implementation:
  [`go/runtime/bus/README.md`](../../../go/runtime/bus/README.md).

<!-- release: track hop-top-cite 0.1.0 -->
