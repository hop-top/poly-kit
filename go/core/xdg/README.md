# xdg

Per-user directory + file resolution for kit-based tools and agents.

Thin wrapper over [`github.com/adrg/xdg`](https://github.com/adrg/xdg) —
XDG Base Directory Specification with OS-native fallbacks (macOS, Windows,
Linux/BSD).

```go
import "hop.top/kit/go/core/xdg"
```

## When to use what

| Need | Use | Example path (Linux) |
|---|---|---|
| User-edited config (yaml, toml) | `ConfigDir` / `ConfigFile` | `~/.config/<tool>/` |
| App data (DBs, caches that survive) | `DataDir` / `DataFile` | `~/.local/share/<tool>/` |
| Disposable cache (regenerable) | `CacheDir` / `CacheFile` | `~/.cache/<tool>/` |
| Volatile state (logs, history) | `StateDir` / `StateFile` | `~/.local/state/<tool>/` |
| Sockets, PID files, IPC | `RuntimeDir` / `RuntimeFile` | `/run/user/<uid>/<tool>/` |
| Installed user binaries | `BinHome` | `~/.local/bin/<tool>/` |
| Read user's Documents/Downloads/etc. | `UserDir(name)` / `UserDirs()` | `~/Documents`, `~/Downloads` |
| Look up org-wide defaults too | `Search*File` | `/etc/xdg/<tool>/...` |
| Discover installed apps / fonts | `ApplicationDirs` / `FontDirs` | `/Applications`, `/usr/share/fonts` |

## API surface

### Base directories — `<base>/<tool>`

```go
xdg.ConfigDir(tool)   // $XDG_CONFIG_HOME/<tool>
xdg.DataDir(tool)     // $XDG_DATA_HOME/<tool>
xdg.CacheDir(tool)    // $XDG_CACHE_HOME/<tool>
xdg.StateDir(tool)    // $XDG_STATE_HOME/<tool>
xdg.RuntimeDir(tool)  // $XDG_RUNTIME_DIR/<tool>
xdg.BinHome(tool)     // $XDG_BIN_HOME/<tool> (~/.local/bin/<tool>)
```

No filesystem side effects. Pair with `MustEnsure` to create.

### File helpers — full path, parent dirs auto-created

```go
xdg.ConfigFile(tool, "app.yaml")   // ~/.config/<tool>/app.yaml
xdg.DataFile(tool, "store.db")     // ~/.local/share/<tool>/store.db
xdg.CacheFile(tool, "index.bin")   // ~/.cache/<tool>/index.bin
xdg.StateFile(tool, "history.log") // ~/.local/state/<tool>/history.log
xdg.RuntimeFile(tool, "agent.sock")// /run/user/<uid>/<tool>/agent.sock
```

`name` may include subdirs (`"sub/app.yaml"`); they're created on demand.

### Search functions — user dir + system XDG\_\*\_DIRS

```go
xdg.SearchConfigFile(tool, "app.yaml")  // checks $XDG_CONFIG_DIRS too
xdg.SearchDataFile(tool, "schema.json")
xdg.SearchCacheFile(tool, "...")
xdg.SearchStateFile(tool, "...")
xdg.SearchRuntimeFile(tool, "...")
```

Returns the first existing match. Lets users / ops drop org-wide defaults
under e.g. `/etc/xdg/<tool>/`.

### User-facing directories — for agents acting on the user's behalf

```go
xdg.Home()                       // user home
xdg.UserDir("documents")         // ~/Documents (case-insensitive)
xdg.UserDir("downloads")         // ~/Downloads ("download" also accepted)
xdg.UserDirs()                   // all 8 well-known dirs as a struct
xdg.FontDirs()                   // [/usr/share/fonts, ~/.local/share/fonts, …]
xdg.ApplicationDirs()            // [/Applications, /usr/share/applications, …]
```

Recognised `UserDir` names: `desktop`, `download(s)`, `documents`, `music`,
`pictures`, `videos`, `templates`, `publicshare` / `public`.

### Helper

```go
dir := xdg.MustEnsure(xdg.DataDir("mytool")) // mkdir -p with 0750; panics on err
```

## Examples

Open the user's config, creating the file path if needed:

```go
path, err := xdg.ConfigFile("mytool", "app.yaml")
// path = ~/.config/mytool/app.yaml; parent created
```

Agent dropping a generated artifact in Downloads:

```go
dl, _ := xdg.UserDir("downloads")
out := filepath.Join(dl, "report-2026-04-27.pdf")
os.WriteFile(out, data, 0o644)
```

Daemon socket under runtime dir:

```go
sock, _ := xdg.RuntimeFile("mytool", "agent.sock")
ln, _ := net.Listen("unix", sock)
```

Honour an org-wide default config when present:

```go
path, err := xdg.SearchConfigFile("mytool", "policy.yaml")
// returns user file if present, else /etc/xdg/mytool/policy.yaml, else error
```

## Platform notes

- **macOS**: `Config*` uses `~/Library/Preferences`; `Data*`/`State*` use
  `~/Library/Application Support` per Apple HIG. Honour `XDG_*_HOME` to
  override (kit + adrg/xdg both respect it).
- **Windows**: `%LocalAppData%\<tool>` (data/state/cache),
  `%AppData%\<tool>` (config). `XDG_*_HOME` honoured when set.
- **Linux/BSD**: pure XDG Base Directory Spec.

Full per-platform path table: see [adrg/xdg README](https://github.com/adrg/xdg#default-locations).
