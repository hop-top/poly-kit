# Config, storage and secrets

Open when loading config files, resolving paths, or storing keys, blobs or
typed records. Long form: [Rust SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/rs-sdk.md#storage).

| Need | Feature | Notes |
|------|---------|-------|
| Layered YAML into a struct | `config` | `config::load`, `options_for_tool`; [snippet](../assets/snippets/config-layers/config-layers.rs) |
| Edit one key keeping comments | `config` | `set`, `unset`; native only |
| Reload on a signal | `config-signal` | unix only |
| PKL schema check | `config-pkl` | `resolve` shells out to `pkl` |
| SQLite connection and pragmas | `sqldb` | bundled SQLite, needs a C compiler |
| Byte-keyed store with expiry | `kv` | builds on `sqldb` |
| Typed JSON store, backup, encryption | `sqlstore`; `sqlstore-blob`, `sqlstore-encrypt` | encryption is opt-in |
| Files and S3 objects | `blob`; `blob-s3` | S3 goes through the guarded client |
| HTTP response cache | `httpcache` | over `kv` |

Precedence, lowest to highest: struct defaults, system, user, project,
extra files, `-c key=value`. Use `#[serde(default)]` so a file with
missing keys decodes. A missing extra `-c` file is an error; the other layers skip.

Gaps taken from the parity page:

- Layered config: no PKL wizard and no config command.
- XDG paths: home and config home only.
- Key-value store: SQLite only; no etcd or TiDB backend.
- Blob store: local and S3 backends; no cross-language corpus pins it.
- SQLite JSON store: only the key derivation is pinned (Rust tests over
  the identity corpus); the store has no corpus of its own.
Secrets and the project registry have no Rust module: use Go, or the
`kit` binary as a sidecar.
