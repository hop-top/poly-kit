# Alias API Reference

Command alias management backed by YAML files. Aliases map
short names to longer command paths, expanding at dispatch time.
Available in Go and TypeScript; Python planned.

For end-user CLI usage (`kit alias add/list/remove`), see
[Getting Started — Aliases](../guides/getting-started-cli.md#aliases).

---

## AliasStore

Single-file store with load/save, CRUD, and first-arg expansion.

| Language | Constructor |
|----------|-------------|
| Go | `alias.NewStore(path) *Store` |
| TS | `new AliasStore(path)` |
| Python | `AliasStore(path)` (planned) |

### Methods

| Method | Go | TS | Description |
|--------|----|----|-------------|
| Load | `Load() error` | `load(): Promise<void>` | Read aliases from YAML |
| Save | `Save() error` | `save(): Promise<void>` | Write to YAML; mkdirs |
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
await s.load();
s.set('ml', 'mission list');
await s.save();

s.expand(['spaced', 'ml', '--format', 'json']);
// => ['spaced', 'mission', 'list', '--format', 'json']
```

### Validation Rules

- Name must be non-empty, no whitespace
- Target must be non-empty
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

### TS / config-embedded (`config.yaml`)

Nested under `aliases:` key; other config keys preserved:

```yaml
format: json
aliases:
  tl: task list
  ml: mission list
```

---

## Expander (TS only)

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

### Expander Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `load` | `load(): AliasMap` | Merged map (seeded < global < local) |
| `expand` | `expand(args): [string[], boolean]` | Rewrite + match flag |
| `validateName` | `validateName(name): void` | Reject empty/whitespace/builtin |

### Flag-aware expansion

`findFirstNonFlag(slice)` skips leading flags before matching
the alias candidate. Flags with `=` are self-contained; short
flags without `=` consume the next element.

```ts
// -c /tmp/config.yaml is skipped; 'tl' is the candidate
e.expand(['tool', '-c', '/tmp/config.yaml', 'tl']);
// => ['tool', '-c', '/tmp/config.yaml', 'task', 'list']
```

---

## CLI Integration (Go)

### Root.Alias — programmatic registration

```go
r := cli.New(cli.Config{Name: "spaced", ...})
cmd := &cobra.Command{Use: "serve"}
r.Cmd.AddCommand(cmd)
r.Alias("s", cmd)  // 's' dispatches to 'serve'
```

Direct children use cobra's `Aliases` field. Nested commands
get a hidden shim added to root.

### Root.LoadAliases — config-driven

Reads `aliases` map from Viper config and registers each entry.
Target paths resolved by walking the command tree.

```go
// config.yaml:
//   aliases:
//     rs: router start

r.LoadAliases()  // registers "rs" -> "router start"
```

### Root.Aliases

Returns a copy of all registered alias-to-target mappings.

```go
m := r.Aliases()  // map[string]string{"s": "serve", ...}
```

### Root.AliasesCmd

Hidden `aliases` subcommand listing active aliases. Supports
`--format` (table/json/yaml).

```sh
spaced aliases
spaced aliases --format json
```

### Collision detection

- Name collides with existing command => error
- Name collides with existing alias => error
- Empty name or whitespace in name => error

---

## Shell Completion Integration

Aliases registered via `Root.Alias` or `Root.LoadAliases` appear
in tab completion alongside real commands. Cobra's built-in
completion walks registered commands including alias shims.

```sh
spaced <TAB>   # shows: deploy, mission, ml, s, serve, ...
```

---

## Storage Location

Default path follows XDG conventions:

```
~/.config/<tool>/aliases.yaml   # Go standalone
~/.config/<tool>/config.yaml    # TS (aliases key)
```

Global and local config paths configurable via `Expander`.

---

## Python (planned)

Will mirror Go's `AliasStore` interface:

```python
store = AliasStore("~/.config/tool/aliases.yaml")
store.load()
store.set("ml", "mission list")
store.save()
store.expand(["ml", "--format", "json"])
```
