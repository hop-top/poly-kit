# config

## What it answers

How does a kit-built Rust tool load one typed config struct from layered YAML files, env and
`-c key=value` overrides, read and write single keys, and hot-reload? Port of Go
`go/core/config`.

## Use it when

- load at startup → `config::load(&mut cfg, &opts)` with `opts` from `options_for_tool("tool")`
- split `-c` tokens → `parse_config_args(&args, &[])` into `extra_config_paths` / `overrides`
- read or write one key → `get`, `list`, `set`, `set_value`, `unset`

## Quick start

```rust
use hop_top_kit::config::{load, Options};

#[derive(Default, serde::Serialize, serde::Deserialize)]
#[serde(default)]
struct Cfg { name: String, port: i64 }

let mut cfg = Cfg::default();
load(&mut cfg, &Options::default()).unwrap();
```

## Contract

- Feature `config`; files, paths, set/unset and reload are off wasm32.
- Merge, precedence, scalar typing and error texts follow Go; deviations are listed in the
  module docs (`hop_top_kit::config`).

## Neighbours

- `yaml/`: the yaml.v3-shaped model underneath
- `hop_top_kit::xdg` (src/xdg/): config home resolution
- `hop_top_kit::bus`: reload events; `EnvLookup` and `Value` implement `ConfigGetter`

## See also

- [`go/core/config`](../../../../go/core/config/README.md), the Go reference
