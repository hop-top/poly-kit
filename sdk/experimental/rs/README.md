# rs

experimental Rust client SDK.

## URI facade

The Rust URI facade is experimental and feature-gated:

```toml
[dependencies]
hop-top-kit = { version = "0.1", features = ["uri"] }
```

`hop_top_kit::uri` delegates to `hop-top-uri`; it does not reimplement URI parsing, action routing, completion, or handler generation. The current workspace uses a local path dependency until `hop-top-uri` is published to crates.io.

