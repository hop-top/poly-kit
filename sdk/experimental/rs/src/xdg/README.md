# xdg

## What it answers

Where are the per-user config and state homes for a tool? Every config or state path in kit-rs
(config, consent, telemetry) resolves here, as Go `go/core/xdg` (adrg/xdg v0.5.3) does.

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

- Feature `config` or `telemetry`, off wasm32. Reads the environment on every call, like Go.
- `$XDG_CONFIG_HOME` wins when absolute (a leading `~` or `$HOME` expands); otherwise
  `~/Library/Application Support` (macOS), `%LOCALAPPDATA%` (Windows), `~/.config`.
- `$XDG_STATE_HOME` likewise; otherwise the same macOS and Windows roots, `~/.local/state`.
- Home is `$HOME`, else `/` (`%USERPROFILE%` first on Windows). On Windows a variable needs a
  volume to count as absolute and only `%USERPROFILE%` expands, as in adrg/xdg.
- `legacy_config_home` / `legacy_state_home`: `~/.config` / `~/.local/state` when no variable is
  set and the default is elsewhere (macOS, Windows), where kit-rs telemetry read before. Readers
  migrate a file found there once and otherwise count it only as a deny; never as a grant.
- Not ported: data/cache/runtime dirs, file and search helpers, the write guard.

## Neighbours

- `hop_top_kit::config`, `hop_top_kit::consent`, `hop_top_kit::telemetry`: the consumers

## See also

- [`go/core/xdg`](../../../../../go/core/xdg/README.md), the Go reference
