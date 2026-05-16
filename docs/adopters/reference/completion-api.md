# Completion API Reference

Dynamic value completion for CLI flags and positional arguments.
Works with cobra (Go), Commander (TS), and Click/Typer (Python).

---

## Completer Interface

Each language exposes a `Completer` that produces suggestions
for a given prefix string.

| Language | Interface |
|----------|-----------|
| Go | `completion.Completer { Complete(ctx, prefix) ([]Item, error) }` |
| TS | `Completer { complete(prefix): CompletionItem[] \| Promise<...> }` |
| Python | `Completer (Protocol) { complete(prefix) -> list[CompletionItem] }` |

### Item / CompletionItem

| Field | Type | Description |
|-------|------|-------------|
| `value` / `Value` | string | Completion text inserted by shell |
| `description` / `Description` | string (optional) | Hint shown alongside value |

**Go**
```go
type Item struct {
    Value       string
    Description string
}
```

**TS**
```ts
interface CompletionItem {
  value: string;
  description?: string;
}
```

**Python**
```python
@dataclass
class CompletionItem:
    value: str
    description: str = ""
```

---

## Built-in Completers

### 1. Static / StaticValues

Fixed list; case-insensitive prefix filter.

**Go**
```go
// With descriptions
completion.Static(
    completion.Item{Value: "leo", Description: "Low Earth Orbit"},
    completion.Item{Value: "geo", Description: "Geostationary"},
)

// Plain strings (no descriptions)
completion.StaticValues("leo", "geo", "lunar")
```

**TS**
```ts
// With descriptions
staticCompleter(
  { value: "leo", description: "Low Earth Orbit" },
  { value: "geo", description: "Geostationary" },
)

// Plain strings
staticValues("leo", "geo", "lunar")
```

**Python**
```python
# With descriptions
static_completer(
    CompletionItem("leo", "Low Earth Orbit"),
    CompletionItem("geo", "Geostationary"),
)

# Plain strings
static_values("leo", "geo", "lunar")
```

### 2. Func / funcCompleter / func_completer

Callback-driven; runs at tab-press time for dynamic results.

**Go**
```go
completion.Func(
    func(ctx context.Context, prefix string) ([]completion.Item, error) {
        return fetchMissions(ctx, prefix)
    },
)
```

**TS**
```ts
funcCompleter((prefix) => {
  return missions
    .filter(m => m.name.startsWith(prefix))
    .map(m => ({ value: m.name, description: m.vehicle }));
})
```

**Python**
```python
func_completer(lambda prefix: [
    CompletionItem(m.name, m.vehicle)
    for m in missions
    if m.name.lower().startswith(prefix.lower())
])
```

### 3. Prefixed / prefixedCompleter / prefixed_completer

`dimension:value` patterns. Suggests `dimension:` before the
colon; delegates to inner completer after.

**Go**
```go
completion.Prefixed("env",
    completion.StaticValues("prod", "staging", "dev"),
)
// Produces: env:prod, env:staging, env:dev
```

**TS**
```ts
prefixedCompleter("env",
  staticValues("prod", "staging", "dev"),
)
```

**Python**
```python
prefixed_completer("env",
    static_values("prod", "staging", "dev"),
)
```

### 4. ConfigKeys / configKeysCompleter / config_keys_completer

Completes from config map keys.

**Go** -- reads all keys from a `*viper.Viper` instance:
```go
completion.ConfigKeys(v) // v is *viper.Viper
```

**TS** -- flattens nested object keys into dot-notation paths:
```ts
configKeysCompleter({ server: { host: "...", port: 8080 } })
// Suggests: server, server.host, server.port
```

**Python** -- completes top-level dict keys:
```python
config_keys_completer({"server.host": "...", "server.port": 8080})
```

### 5. File / fileCompleter / file_completer

File glob with optional extension filter.

**Go** -- signals cobra directive (shell handles actual glob):
```go
completion.File(".json", ".yaml") // filter by extension
completion.File()                 // all files
```

**TS** -- returns `__file__` marker; `__complete` handler
translates to shell-native directive:
```ts
fileCompleter(".json", ".yaml")
fileCompleter() // all files
```

**Python** -- performs actual `os.listdir` at tab-press time:
```python
file_completer(".json", ".yaml")
file_completer() # all files
```

### 6. Dir / dirCompleter / dir_completer

Directory-only completion.

**Go**
```go
completion.Dir()
```

**TS**
```ts
dirCompleter()
```

**Python**
```python
dir_completer()
```

---

## Registry

Maps flag names and positional arg positions to completers.
Available in all three languages with identical semantics.

| Method | Go | TS | Python |
|--------|----|----|--------|
| Create | `completion.NewRegistry()` | `new CompletionRegistry()` | `CompletionRegistry()` |
| Flag | `r.Register(flag, c)` | `r.register(flag, c)` | `r.register(flag, c)` |
| Arg | `r.RegisterArg(cmd, pos, c)` | `r.registerArg(cmd, pos, c)` | `r.register_arg(cmd, pos, c)` |
| Lookup flag | `r.ForFlag(flag)` | `r.forFlag(flag)` | `r.for_flag(flag)` |
| Lookup arg | `r.ForArg(cmd, pos)` | `r.forArg(cmd, pos)` | `r.for_arg(cmd, pos)` |

---

## Framework Bridge

How completers connect to each framework's native mechanism.

### Go -- cobra

`BindFlag` and `BindArgs` wire completers directly into cobra:

```go
// Flag completion
completion.BindFlag(cmd, "orbit", completion.StaticValues(
    "leo", "geo", "lunar",
))

// Positional arg completion
completion.BindArgs(cmd, completion.Func(
    func(ctx context.Context, prefix string) ([]completion.Item, error) {
        return lookupMissions(ctx, prefix)
    },
))
```

Internally:
- `BindFlag` calls `cmd.RegisterFlagCompletionFunc`
- `BindArgs` sets `cmd.ValidArgsFunction`
- `File`/`Dir` completers emit shell directives
  (`ShellCompDirectiveFilterFileExt`, `ShellCompDirectiveFilterDirs`)
- All others emit `ShellCompDirectiveNoFileComp`

### TS -- Commander

Attach a `CompletionRegistry` to the command object:

```ts
const reg = new CompletionRegistry();
reg.register("--orbit", staticValues("leo", "geo"));
reg.registerArg("launch", 0, missionCompleter);
(cmd as any).__completionRegistry = reg;
```

The `__complete` handler (from `completion.ts`) reads
`__completionRegistry` at tab-press time.

### Python -- Click/Typer

Bridge via `to_click_shell_complete`:

```python
from hop_top_kit.completion import (
    static_values, to_click_shell_complete,
)

orbit_c = static_values("LEO", "GTO", "GEO")

@app.command()
def launch(
    orbit: str = typer.Option(
        None, "--orbit",
        shell_complete=to_click_shell_complete(orbit_c),
    ),
): ...
```

`to_click_shell_complete` returns a callable matching Click's
`(ctx, param, incomplete) -> list[ClickCompletionItem]` signature.

---

## Example: Adding Completions (spaced launch)

Full working example from the `spaced` demo CLI.

### Go

```go
import "hop.top/kit/go/console/cli/completion"

// Flag: --orbit
completion.BindFlag(cmd, "orbit", completion.StaticValues(
    "leo", "geo", "lunar", "helio", "tbd",
))

// Positional arg: mission name (dynamic lookup)
completion.BindArgs(cmd, completion.Func(
    func(_ context.Context, prefix string) ([]completion.Item, error) {
        matches := data.SearchMissions(prefix)
        items := make([]completion.Item, len(matches))
        for i, m := range matches {
            items[i] = completion.Item{
                Value:       m.ID,
                Description: m.Name + " (" + m.Vehicle + ")",
            }
        }
        return items, nil
    },
))
```

### TS

```ts
import {
  CompletionRegistry, staticValues, funcCompleter,
} from '@hop-top/kit/completion-values';

const reg = new CompletionRegistry();
reg.register('--orbit',
  staticValues('leo', 'geo', 'lunar', 'helio', 'tbd'));
reg.registerArg('launch', 0, funcCompleter((prefix) => {
  const lp = prefix.toLowerCase();
  return MISSIONS
    .filter(m => m.name.toLowerCase().startsWith(lp))
    .map(m => ({ value: m.name, description: m.vehicle }));
}));
(cmd as any).__completionRegistry = reg;
```

### Python

```python
from hop_top_kit.completion import (
    CompletionItem, CompletionRegistry,
    static_values, func_completer, to_click_shell_complete,
)

_orbit_c = static_values("LEO", "GTO", "GEO", "SSO", "Heliocentric")

def _mission_complete(prefix: str) -> list[CompletionItem]:
    low = prefix.lower()
    return [
        CompletionItem(m.name, m.vehicle)
        for m in MISSIONS
        if m.name.lower().startswith(low)
    ]

reg = CompletionRegistry()
reg.register("--orbit", _orbit_c)
reg.register_arg("launch", 0, func_completer(_mission_complete))

# Wire into Typer/Click
@app.command()
def launch(
    mission: str = typer.Argument(
        ...,
        shell_complete=to_click_shell_complete(
            reg.for_arg("launch", 0),
        ),
    ),
    orbit: str = typer.Option(
        None, "--orbit",
        shell_complete=to_click_shell_complete(_orbit_c),
    ),
): ...
```

---

## Source Files

| Language | Path |
|----------|------|
| Go | `go/console/cli/completion/completion.go` |
| TS | `sdk/ts/src/completion-values.ts` |
| Python | `sdk/py/hop_top_kit/completion.py` |
