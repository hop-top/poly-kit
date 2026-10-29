# Alias API Reference

Command alias management backed by YAML files. Aliases map
short names to longer command paths, expanding at dispatch time.
Available in Go, TypeScript, Python and Rust. Bridging, name
validation and cycle detection are pinned across the four by the
[alias-v1 contract](../../../contracts/alias-v1/README.md).

For end-user CLI usage (`kit alias add/list/remove`), see
[Getting Started — Aliases](../guides/getting-started-cli.md#aliases).

---

## What each port ships

| | Go | TS | Python | Rust |
|---|---|---|---|---|
| Single-file store | `alias.NewStore(path)` | `new AliasStore(path)` | `load_from` / `save_to` | — |
| Three-tier expander | — | `Expander` | `Expander` | — |
| Bridge onto the command tree | `Root.Alias`, `LoadAliases`, `LoadAliasStore` | `bridgeAliases` | `bridge_to_click` | `Root::alias`, `Root::alias_with` |
| Standalone name validation | `alias.ValidateName` | `validateAliasName` | `validate_alias_name` | `validate_alias_name` |
| Cycle detection | `alias.DetectCycles` | `detectCycles` | `detect_cycles` | `detect_cycles` |

---

## AliasStore

Single-file store with load/save, CRUD, and first-arg expansion.

| Method | Go | TS | Description |
|--------|----|----|-------------|
| Load | `Load() error` | `load(): void` | Read aliases from YAML |
| Save | `Save() error` | `save(): void` | Write to YAML; mkdirs |
| Set | `Set(name, target) error` | `set(name, target)` | Add/update alias |
| Remove | `Remove(name) error` | `remove(name)` | Delete alias |
| Get | `Get(name) (string, bool)` | `get(name): string?` | Lookup single alias |
| All | `All() map[string]string` | `all(): AliasMap` | Copy of all aliases |
| Expand | `Expand(args) []string` | `expand(args): string[]` | Replace first-arg match |

**Go**
```go
s := alias.NewStore("~/.config/spaced/aliases.yaml")
s.Load()
s.Set("ml", "mission list")
s.Save()

s.Expand([]string{"ml", "--format", "json"})
// => ["mission", "list", "--format", "json"]
```

**TS**
```ts
const s = new AliasStore('~/.config/spaced/config.yaml');
s.load();
s.set('ml', 'mission list');
s.save();

s.expand(['spaced', 'ml', '--format', 'json']);
// => ['spaced', 'mission', 'list', '--format', 'json']
```

**Python** has no store class; `load_from(path)` reads the
`aliases:` map of a config file and `save_to(path, aliases)` writes
it back, keeping other keys.

### Store rules

- Go `Set`: name must be non-empty, no whitespace; target non-empty
- Go `Remove` returns error if name not found; TS silently no-ops
- `All()` returns a copy; mutating it does not affect the store

---

## YAML Format

### Go standalone store (`aliases.yaml`)

Flat map — keys are alias names, values are targets:

```yaml
d: deploy
ml: mission list
ls: fleet list
```

### TS / Python config-embedded (`config.yaml`)

Nested under `aliases:` key; other config keys preserved:

```yaml
format: json
aliases:
  tl: task list
  ml: mission list
```

---

## Expander (TS, Python)

Three-tier alias resolution with priority:
**seeded < global < local**.

```ts
const e = new Expander({
  globalPath: '~/.config/tool/config.yaml',
  localPath:  '.tool/config.yaml',
  seededAliases: { setup: 'config interactive' },
  builtins: new Set(['task', 'config', 'help']),
});

const [args, matched] = e.expand(['tool', 'tl', '--mine']);
// => [['tool', 'task', 'list', '--mine'], true]
```

```python
e = Expander(Config(
    global_path="~/.config/tool/config.yaml",
    local_path=".tool/config.yaml",
    seeded_aliases={"setup": "config interactive"},
    builtins={"task", "config", "help"},
))
args, matched = e.expand(["tool", "tl", "--mine"])
```

| Method | TS | Python | Description |
|--------|----|--------|-------------|
| load | `load(): AliasMap` | `load() -> dict` | Merged map (seeded < global < local) |
| expand | `expand(args): [string[], boolean]` | `expand(args) -> (list, bool)` | Rewrite + match flag |
| validate name | `validateName(name): void` | `validate_name(name)` | The standalone validator with the config's builtins |

### Flag-aware expansion

`findFirstNonFlag(slice)` (Python `find_first_non_flag`) skips
leading flags before matching the alias candidate. Flags with `=`
are self-contained; short flags without `=` consume the next
element (TS: long flags too).

```ts
// -c /tmp/config.yaml is skipped; 'tl' is the candidate
e.expand(['tool', '-c', '/tmp/config.yaml', 'tl']);
// => ['tool', '-c', '/tmp/config.yaml', 'task', 'list']
```

---

## Name validation

Checks a name without a store, config file or command tree. Rules
run in order; the first failure is reported with its kind:

| Kind | Fails when |
|------|------------|
| `empty` | the name is `""` |
| `whitespace` | the name holds any Unicode White_Space character |
| `builtin` | the name is one of the given builtins (exact match) |
| `duplicate` | the name is already a key of the given alias map |

```go
err := alias.ValidateName("tl", alias.NameRules{
    Builtins: []string{"task", "help"},
    Existing: store.All(),
})
var ne *alias.NameError
if errors.As(err, &ne) { /* ne.Kind == alias.NameBuiltin, ... */ }
```

```ts
validateAliasName('tl', { builtins: ['task', 'help'], existing: store.all() });
// throws AliasNameError (err.kind: 'empty' | 'whitespace' | 'builtin' | 'duplicate')
```

```python
validate_alias_name("tl", builtins={"task", "help"}, existing=aliases)
# raises AliasNameError (a ValueError) with .kind
```

```rust
validate_alias_name("tl", &AliasNameRules { builtins: &["task", "help"], existing: Some(&map) })?;
// Err(AliasNameError { kind: AliasNameErrorKind::Builtin, .. })
```

Error texts stay per port: Go and Rust use `Root.Alias`'s texts
(`alias "x" collides with command "x"`), TS and Python the
Expander's (`alias "x" conflicts with a built-in command`).
`Root.Alias` itself still checks only space, tab and newline.

## Cycle detection

An alias points at another when the first word of its target is
also an alias name. `DetectCycles` / `detectCycles` /
`detect_cycles` return every cycle as a path that starts at its
smallest name and returns to it, sorted by first name:

```ts
detectCycles({ a: 'b --x', b: 'c', c: 'a', d: 'deploy' });
// => [['a', 'b', 'c', 'a']]
```

An alias that only leads into a cycle is not reported as part of
it. No bridge rejects cycles; call it when you load aliases.

---

## Bridging onto the command tree

Each port bridges aliases onto its framework. Without options a port
bridges as it always has; the defaults differ:

| | Go | TS | Python | Rust |
|---|---|---|---|---|
| Direct child / single-word target | cobra alias | Commander alias | `get_command` returns the target | clap alias |
| Deeper / multi-word target | hidden shim, flagless | hidden proxy | `get_command` returns the first word's command | hidden shim, flagless |
| `<alias> --help` on a proxy | the proxy's own help | the target's help | (no proxy) | the proxy's own help |
| Dispatch | shim runs the target's `RunE` | proxy re-enters the program with the target path | Click runs the returned command | caller, through `Root::alias_target` |

### Options

Setting any option registers **every** alias as a proxy: a root
command named after the alias, even where the framework has a native
alias. Unset options keep the proxy defaults above.

| Option | Go | TS | Python | Rust |
|--------|----|----|--------|------|
| visibility (default hidden) | `WithAliasHidden(bool)` | `hidden` | `hidden=` | `.hidden(bool)` |
| description (default `Alias for <target>`) | `WithAliasDescribe(func(name, target) string)` | `describe(name, target)` | `describe=` | `.describe(\|name, target\| ..)` |
| help | `WithAliasHelp(AliasHelpOwn \| AliasHelpPassthrough)` | `helpOption: true` (own) / `false` | `help_mode="own" \| "passthrough"` | `.help(AliasHelp::Own \| Passthrough)` |
| custom dispatch | `WithAliasDispatch(func(cmd, name, target, args) error)` | `dispatch(name, target, cmd)` | `dispatch=f(name, target, args)` | — |
| register-only | `WithAliasRegisterOnly()` | `dispatch: false` | `dispatch=False` | always |

A custom dispatch handler gets every argument after the alias
unparsed (TS: `cmd.args`). With help passthrough, `--help` reaches
the handler as an argument; with own help the proxy prints its help.
A proxy that dispatches to its target takes the target's flags (Go
copies them onto the proxy; TS, Python and Rust re-parse the target
path). Rust has no dispatch hook: `Root::alias_with` proxies take
every argument (flags included) in `SHIM_ARGS`, and the caller
re-parses the target path from `Root::alias_target`.

**Go**
```go
err := root.LoadAliasStore(store,
    cli.WithAliasHidden(false),
    cli.WithAliasDescribe(func(name, target string) string { return "→ " + target }),
    cli.WithAliasHelp(cli.AliasHelpOwn),
)
```

**TS** — `bridgeAliases(program, store | map, options?)`
```ts
bridgeAliases(program, { d: 'deploy', tl: 'task list' }, {
  hidden: false,
  describe: (name, target) => `→ ${target}`,
  helpOption: true,
  dispatch: async (name, target, cmd) => runAlias(target, cmd.args),
});
```

**Python** — `bridge_to_click(group, path | mapping, **options)`
```python
bridge_to_click(app, {"d": "deploy"}, hidden=False,
                describe=lambda name, target: f"→ {target}")
```

**Rust**
```rust
root.alias_with("d", "deploy", &AliasOptions::new().hidden(false).help(AliasHelp::Passthrough))?;
let m = root.command().get_matches();
if let Some((name, sub)) = m.subcommand() {
    if let Some(target) = root.alias_target(name) {
        let rest = sub.get_many::<String>(SHIM_ARGS).into_iter().flatten();
        // re-parse `<tool> <target...> <rest...>` and dispatch
    }
}
```

### Go: Root.Alias, LoadAliases, LoadAliasStore

```go
r := cli.New(cli.Config{Name: "spaced", ...})
cmd := &cobra.Command{Use: "serve"}
r.Cmd.AddCommand(cmd)
r.Alias("s", cmd)  // 's' dispatches to 'serve'
```

`LoadAliases(opts...)` reads the `aliases` map from Viper config;
`LoadAliasStore(store, opts...)` registers every entry of an
`alias.Store`. Both resolve target paths by walking the command tree
and aggregate unknown targets and collisions into one error.

`Root.Aliases()` returns a copy of the registered mappings.
`Root.AliasCmd(store)` returns a visible `alias` command group
(list / add / delete) for user-managed aliases, its leaves carrying
kit's Layer-A annotations. `Root.AliasesCmd()` is a hidden `aliases`
listing rendered through `output.Dispatch`, so it honours `--format`.

Registration errors on a name that is empty or holds whitespace,
collides with an existing command, or is already an alias.

---

## Shell Completion Integration

Aliases registered through any bridge appear in tab completion at
the root alongside real commands; Go, TS and Rust describe them as
`alias for <target>`.

```sh
spaced <TAB>   # shows: deploy, mission, ml, s, serve, ...
```

---

## Storage Location

Default path follows XDG conventions:

```
~/.config/<tool>/aliases.yaml   # Go standalone
~/.config/<tool>/config.yaml    # TS / Python (aliases key)
```

Global and local config paths configurable via `Expander`.
