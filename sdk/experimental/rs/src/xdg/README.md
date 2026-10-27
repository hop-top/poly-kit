# xdg

## What it answers

Where are the per-user config and state homes for a tool? Only the pieces the config loader and
consent need from Go `go/core/xdg` are ported: home, `<config home>/<tool>`, `<state home>/<tool>`.

## Use it when

- building a user config path → `xdg::raw_config_dir("tool").join("config.yaml")`
- you need the user's home → `xdg::home()`
- building a per-tool state path → `xdg::raw_state_dir("tool")`

## Quick start

```rust
let dir = hop_top_kit::xdg::raw_config_dir("mytool");
assert!(dir.ends_with("mytool"));
```

## Contract

- Feature `config`, off wasm32. Reads the environment on every call, like Go.
- `$XDG_CONFIG_HOME` wins when absolute (a leading `~` or `$HOME` expands); otherwise
  `~/Library/Application Support` (macOS), `%LOCALAPPDATA%` (Windows), `~/.config`.
- `$XDG_STATE_HOME` likewise; otherwise the same macOS and Windows roots, `~/.local/state`.
- Home is `$HOME`, else `/` (`%USERPROFILE%` first on Windows).
- Not ported: data/cache/runtime dirs, file and search helpers, the write guard.

## Neighbours

- `hop_top_kit::config` (src/config/), `hop_top_kit::consent` (src/consent/): the consumers

## See also

- [`go/core/xdg`](../../../../../go/core/xdg/README.md), the Go reference
