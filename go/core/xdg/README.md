# xdg

## What it answers

Where a tool's config, data, cache, state, runtime and bin files live for
this user on this OS, and where the user's own Documents, Downloads and
similar directories are. Whether a resolved path may be touched is
`go/core/scope`; parsing the file once resolved is `go/core/config`.

Thin wrapper over [`github.com/adrg/xdg`](https://github.com/adrg/xdg): the
XDG Base Directory Specification with OS-native fallbacks.

## Use it when

- resolve a base dir, no I/O → `xdg.ConfigDir`, `DataDir`, `CacheDir`, `StateDir`, `RuntimeDir`, `BinHome`
- resolve a file and create its parents → `xdg.ConfigFile`, `DataFile`, `CacheFile`, `StateFile`, `RuntimeFile`
- honour org-wide defaults under `/etc/xdg` → `xdg.SearchConfigFile` and the other `Search*File`
- act on the user's behalf → `xdg.Home`, `UserDir(name)`, `UserDirs`, `FontDirs`, `ApplicationDirs`
- create a resolved dir → `xdg.MustEnsure(xdg.DataDir("mytool"))`

## Quick start

```go
path, err := xdg.ConfigFile("mytool", "app.yaml")
// path = ~/.config/mytool/app.yaml; parent created
```

## Contract

- `*Dir` and `BinHome` have no filesystem side effects; `*File` creates parent directories (`MkdirAll`), and `name` may include subdirs.
- `Search*File` returns the first existing match across the user dir and `$XDG_*_DIRS`, else an error.
- `UserDir` names are case-insensitive: `desktop`, `download(s)`, `documents`, `music`, `pictures`, `videos`, `templates`, `publicshare` / `public`.
- `MustEnsure` does `mkdir -p` with 0750 and panics on error.
- Every function is goroutine-safe; the package serializes the `adrg/xdg` refresh and the resolve that follows it, which the library alone does not.
- The lock covers the resolve, so `*File` serializes `MkdirAll` and `Search*File` serializes its stats across goroutines. Hoist resolves out of hot loops.
- The environment is read on every call: `t.Setenv` takes effect on the next call, with no cache and no refresh function.
- macOS puts `Config*` under `~/Library/Preferences` and `Data*` / `State*` under `~/Library/Application Support`; Windows uses `%LocalAppData%` and `%AppData%`; Linux and BSD are pure XDG. `XDG_*_HOME` overrides all three.

## Neighbours

- `go/core/scope`: whether a resolved path may be read, written or executed; `xdg.SetGuard` wires the hook.
- `go/core/config`: loading and merging the files these helpers locate.
- `go/core/xdg/scopetest`: test-only helper package for scope-guarded xdg tests.

## See also

- [XDG reference](../../../docs/adopters/reference/xdg.md): full API surface, examples, concurrency cost table, platform notes
- [Config reference](../../../docs/adopters/reference/config.md)
- [adrg/xdg default locations](https://github.com/adrg/xdg#default-locations)
