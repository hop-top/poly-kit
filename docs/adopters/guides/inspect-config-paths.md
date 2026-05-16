# Inspect config paths

See exactly which config files a kit-built CLI reads, in what order,
and which value wins.

## Who this is for

Developers debugging "why is this value winning?" in a kit-built CLI
(`kit`, `tlc`, `aps`, `ctxt`, etc.). Use this when a `config get`
returns something you didn't expect, or when you need to know which
file to edit to override a setting.

## Before you begin

You need:

- A kit-built CLI on `$PATH` that exposes the standard `config`
  subcommand (every kit-built tool from kit ≥ 0.4.0).
- Read access to the directories the tool searches (XDG dirs, `/etc`,
  the project tree).

Two commands are in scope here. They are nouns, not verbs:

| Command | Returns |
|---|---|
| `<tool> config path` | The single highest-precedence config file that exists today. |
| `<tool> config paths` | The full ordered chain (every searched location, with `exists`/`scope`/`source` metadata). |

Order, lowest precedence first (matches `core/config.Loader`):

1. Built-in defaults (no file)
2. System: `/etc/<tool>/config.yaml`
3. User: `$XDG_CONFIG_HOME/<tool>/config.yaml`
4. Project: `./.<tool>.yaml` (in CWD or first ancestor)
5. `--config <path>` flag (explicit override)
6. Env vars: `<TOOL>_<KEY>`
7. CLI flags

## Recommended path

Run `<tool> config paths` first. It is the answer to almost every
"where does this value come from?" question.

```bash
tlc config paths
```

```
SCOPE     SOURCE                                             EXISTS  WINS
default   (built-in)                                         -       -
system    /etc/tlc/config.yaml                               no      -
user      /Users/jadb/.config/tlc/config.yaml                yes     -
project   /Users/jadb/.w/.../my-repo/.tlc/config.yaml        yes     *
flag      (--config not set)                                 -       -
```

The row with `WINS = *` is the highest-precedence file that exists.
That is the file to edit (or override via env/flag) to change a
value.

## Steps

### 1. List the chain

```bash
<tool> config paths
```

Add `--format json` for scripting:

```bash
<tool> config paths --format json
```

```json
[
  {"scope":"default","source":"(built-in)","exists":false,"wins":false},
  {"scope":"user","source":"/Users/jadb/.config/tlc/config.yaml","exists":true,"wins":false},
  {"scope":"project","source":"/Users/jadb/.w/repo/.tlc/config.yaml","exists":true,"wins":true}
]
```

### 2. Get the winning path only

When you just want the file to edit:

```bash
<tool> config path
```

Prints a single line; nothing else. Pipeable:

```bash
$EDITOR "$(tlc config path)"
```

### 3. Filter by scope

`--from <scope>` returns only one row from the chain:

```bash
<tool> config paths --from user      # only the user-scoped path
<tool> config paths --from project   # only the project-scoped path
```

`--from` accepts: `default`, `system`, `user`, `project`, `flag`.

### 4. Inspect the value source

Once you know which file wins, read it directly:

```bash
cat "$(tlc config path)"
```

If the key you're looking for isn't in the winning file, walk down
the chain. Use `--from user`, then `--from system`, until you find
the file that defines the key.

## Verify the result

Pick a key whose value surprised you. Then:

```bash
<tool> config get <key>            # current effective value
<tool> config paths                # see the chain
grep <key> "$(<tool> config path)" # is it in the winning file?
```

If `grep` finds it, the winning file owns the value. If not, the
value comes from a lower-precedence file, an env var, or a built-in
default — walk down the chain to find the source.

## Troubleshooting

### `config: unknown command`

The tool is built against kit < 0.4.0 (before the shared
subcommand). Upgrade the tool, or fall back to manual inspection:

```bash
ls /etc/<tool>/                          # system
ls "$XDG_CONFIG_HOME/<tool>/"            # user
find . -maxdepth 5 -name ".<tool>.yaml"  # project
```

### `paths` shows `exists: yes` for a file you deleted

The CLI caches nothing — it stats each path on every call. Re-run
the command. If still wrong, the file actually exists; check
permissions or symlinks.

### Project scope reports `exists: no` but `.<tool>.yaml` is in CWD

Project lookup walks from CWD upward looking for `.<tool>.yaml` (or
`.<tool>/config.yaml`). If you're in a sibling directory or the
file is named differently, project scope won't find it. Either:

- `cd` into the project root, or
- Use `--config <path>` for an explicit override.

### Two project files exist (e.g. `.tlc.yaml` and `.tlc/config.yaml`)

Only one wins per scope. Convention is `.<tool>/config.yaml` is
preferred over `.<tool>.yaml`. Check `paths --from project` to see
which the CLI resolved to.

### Env var override isn't reflected

`config paths` shows files only — env vars and `--config` flag
overrides are layered on top by `viper`. Run `<tool> config get
<key>` to see the effective value, which includes env + flag
layers.

## Related pages

- [cli-api-reference.md](../reference/cli-api-reference.md) — `Config inspection`
  section for `--format` and `--from` flags.
- `~/.ops/docs/cli-conventions-with-kit.md` §7 — the layering rules
  these paths implement.
- `~/.ops/runbooks/debug-config-precedence.md` — full runbook for
  resolving "why is this value winning" incidents.
