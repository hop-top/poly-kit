# xdg

## What it answers

Where is the per-user config home for a tool? Only the pieces the config loader needs from
Go `go/core/xdg` are ported: the home directory and `<config home>/<tool>`.

## Use it when

- building a user config path → `xdg::raw_config_dir("tool").join("config.yaml")`
- you need the user's home → `xdg::home()`

## Quick start

```rust
let dir = hop_top_kit::xdg::raw_config_dir("mytool");
assert!(dir.ends_with("mytool"));
```

## Contract

- Feature `config`, off wasm32. Reads the environment on every call, like Go.
- `$XDG_CONFIG_HOME` wins when absolute (a leading `~` or `$HOME` expands); otherwise
  `~/Library/Application Support` (macOS), `%LOCALAPPDATA%` (Windows), `~/.config`.
- Home is `$HOME`, else `/` (`%USERPROFILE%` first on Windows).
- Not ported: data/cache/state/runtime dirs, file and search helpers, the write guard.

## Neighbours

- `hop_top_kit::config` (src/config/): the only consumer

## See also

- [`go/core/xdg`](../../../../../go/core/xdg/README.md), the Go reference
