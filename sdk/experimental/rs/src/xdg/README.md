# xdg

## What it answers

Where are the per-user config and data homes for a tool? Only the pieces the config loader and the
upgrade runner need from Go `go/core/xdg` are ported: the home directory, `<config home>/<tool>` and
`<data home>/<tool>`.

## Use it when

- building a user config path → `xdg::raw_config_dir("tool").join("config.yaml")`
- building a user data path → `xdg::raw_data_dir("tool")`
- you need the user's home → `xdg::home()`

## Quick start

```rust
let dir = hop_top_kit::xdg::raw_config_dir("mytool");
assert!(dir.ends_with("mytool"));
```

## Contract

- Feature `config` or `upgrade`, off wasm32. Reads the environment on every call, like Go.
- `$XDG_CONFIG_HOME` wins when absolute (a leading `~` or `$HOME` expands); otherwise
  `~/Library/Application Support` (macOS), `%LOCALAPPDATA%` (Windows), `~/.config`.
- `$XDG_DATA_HOME` under the same rule; otherwise as config on macOS and Windows, `~/.local/share`.
- Home is `$HOME`, else `/` (`%USERPROFILE%` first on Windows).
- Not ported: cache/state/runtime dirs, file and search helpers, the write guard.

## Neighbours

- `hop_top_kit::config` (src/config/): config home
- `hop_top_kit::upgrade` (src/upgrade/): data home, for version files and backups

## See also

- [`go/core/xdg`](../../../../../go/core/xdg/README.md), the Go reference
