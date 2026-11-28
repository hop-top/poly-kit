# Config, storage and secrets

Open when a tool reads layered config, needs XDG directories, or keeps small
state in SQLite. Reference: [Python SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/py-sdk.md).
Every module here has cell `unverified` in [coverage](coverage.md): no
shared corpus pins it to Go, so test before promising identical behaviour.

## Decisions

- Layered config: `hop_top_kit.config`. `load(dst, Options(...))` merges
  YAML system, user, project, then an `env_override(dict)` callable you
  write. Dicts merge key by key, lists and scalars are replaced, a missing
  file is skipped, bad YAML raises `yaml.YAMLError`. The result is a plain
  dict: validate it yourself (Pydantic is already a dependency).
- Only YAML. Another format or a framework's own settings (Django, Flask):
  parse it yourself and pass the dict, or call `load` with a YAML layer.
- Directories: `config_dir`, `data_dir`, `cache_dir`, `state_dir` return a
  path string and create nothing; `must_ensure(path)` creates it. They honour
  `XDG_CONFIG_HOME` and its siblings, else the platform's directory.
- Small persisted state: `open_store(path)` returns a `Store` with
  `put(key, value)` and `get(key, type)`; `Options` sets a TTL.

## Not here

Secrets, key-value store, blob store, HTTP cache and project registry have no
module in this port: [other ports](other-ports.md). Do not store secrets in
the config dict or the SQLite store.
