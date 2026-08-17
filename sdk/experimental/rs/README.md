# rs

experimental Rust client SDK.

## Modules

- [`src/id/`](src/id/) — TypeID primitive (cross-language; see
  [ADR 0001](../../../docs/adr/0001-typeid-primitive.md))

## URI facade

The Rust URI facade is experimental and feature-gated:

```toml
[dependencies]
hop-top-kit = { version = "0.1", features = ["uri"] }
```

`hop_top_kit::uri` delegates to `hop-top-cite`; it does not reimplement
URI parsing, action routing, completion, or handler generation.
`hop-top-cite` is published on crates.io.

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
| `KIT_TELEMETRY_MODE` | `off | anon | full` (SDK-level). |
| `<APP>_TELEMETRY_MODE` | Overrides KIT-level when `KIT_APP_PREFIX` is set. |
| `KIT_TELEMETRY_ENDPOINT` | HTTPS sink target (used by `ClientOptions::from_env`). |
| `KIT_TELEMETRY_SINK` | `https | jsonl` (used by `ClientOptions::from_env`). |
| `KIT_TELEMETRY_SINK_FILE` | JSONL path (used by `ClientOptions::from_env`). |
| `KIT_TELEMETRY_QUEUE_SIZE` | Bounded channel capacity, defaults to 1024. |
| `XDG_CONFIG_HOME` | Locates the consent file (`<HERE>/kit/config.yaml` at `kit.telemetry.consent`; legacy `<HERE>/kit/telemetry.yaml` read as fallback). |
| `XDG_STATE_HOME` | Locates the install_id file (`<HERE>/kit/telemetry/installation_id`). |

### Cross-references

- Schema doc: [`sdk/docs/telemetry-event-schema.md`](../../docs/telemetry-event-schema.md).
- Go canonical implementation:
  [`go/runtime/telemetry/README.md`](../../../go/runtime/telemetry/README.md).

<!-- release: track hop-top-cite 0.1.0 -->
