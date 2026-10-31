# Use the project registry

> Audience: Go authors of hop.top-family tools that need to resolve a short project name to a
> directory, or to register one. Package: `hop.top/kit/go/core/projects`. Package README:
> [projects](../../../go/core/projects/README.md).

The registry is one YAML file mapping short names (`ops`, `kit`) to a path and an optional
startup command. Two kinds of tool share it: writers add entries when a workspace space is
added or synced (`wsm`), and readers resolve a name to a directory (`rux connect <name>`, or any
tool's `-C <name>`). The package gives both sides a locked, atomic API and does no logging.

## What the file looks like

```yaml
schema: 2
projects:
  ops:
    path: /Users/alice/src/ops
    startup_cmd: zsh
    source: wsm
  notes:
    path: /Users/alice/notes
    source: manual
```

`projects.DefaultPath()` returns the location: `rux/projects.yaml` under the user's config
directory. `XDG_CONFIG_HOME` overrides the base on every platform, which is how tests isolate
it. `SchemaVersion` is `2` (version 2 added the optional `stage` block).

## 1. Read the registry

```go
f, err := projects.Read()
if err != nil {
    return err
}
e, ok := f.Projects["ops"]
```

`Read` returns:

| Situation | Result |
|---|---|
| file absent | empty `File`, nil error (a fresh machine is not a failure) |
| YAML unparseable | `projects.ErrMalformed` |
| `schema` newer than this build's `SchemaVersion` | `projects.ErrSchemaUnsupported` |
| otherwise | the parsed `File` |

Branch with `errors.Is`. The package never logs, so how loudly to report a bad file is your
decision.

A present but empty `source:` value loads as `SourceWSM`; a missing key reads as the empty string.

## 2. Resolve `-C <name>` through the registry

`cli.Config.ChdirResolver` is called when `-C` is given something that is not an existing
directory. Plug the registry in:

```go
func resolveProject(target string) (string, error) {
    f, err := projects.Read()
    if err != nil {
        return "", err
    }
    e, ok := f.Projects[target]
    if !ok {
        return "", fmt.Errorf("no project %q in registry", target)
    }
    return e.Path, nil
}

root := cli.New(cli.Config{
    Name: "tool", Version: "1.0.0", Short: "...",
    ChdirResolver: resolveProject,
})
```

Return an error for a name you do not know. A resolver that guesses a nearest match can send a
command into the wrong project; fail on unknown names instead.

## 3. Register or update a project

```go
err := projects.Write("kit", projects.Entry{
    Path:       "/Users/alice/src/kit",
    StartupCmd: "zsh",
    Source:     projects.SourceManual,
})
```

`Write` upserts one name and leaves every other entry untouched, whatever its `Source`. A
manual entry therefore survives any number of `wsm` rewrites. An empty `Source` is stored as
`SourceWSM`, so set `SourceManual` for anything a person added by hand.

`Delete(name)` removes one entry and ignores a name that is not there.

Both return `ErrMalformed` or `ErrSchemaUnsupported`, without modifying the file, when the existing
registry cannot be read or is newer than this build.

Both calls take an exclusive `flock` on `projects.yaml.lock` for the whole read-modify-write and
publish the result by writing a temp file and renaming it. Concurrent processes and goroutines
are safe, and a crash cannot leave a half-written file.

## 4. Test against a temp registry

Point `XDG_CONFIG_HOME` at a temp dir before calling anything:

```go
func TestResolve(t *testing.T) {
    t.Setenv("XDG_CONFIG_HOME", t.TempDir())

    if err := projects.Write("ops", projects.Entry{Path: "/src/ops"}); err != nil {
        t.Fatal(err)
    }
    got, err := resolveProject("ops")
    if err != nil || got != "/src/ops" {
        t.Fatalf("got %q, %v", got, err)
    }
}
```

## Pitfalls

- `Write` and `Delete` refuse to touch a bad file. If the existing file is malformed, or its
  `schema` is newer than your build understands, they return `ErrMalformed` or
  `ErrSchemaUnsupported` and leave the file as it was, so no entries are lost. Handle those errors
  like `Read`'s; a missing file is still treated as empty.
- Unknown YAML fields are not preserved on rewrite. Only `path`, `startup_cmd`, `source` and
  `stage` survive a `Write`.
- Do not hand-edit the `stage` block. The `stage` package (`go/core/stage`) owns it, and `stage`
  commands read and write it through this registry.
- Names are map keys. Two writers that register the same name overwrite each other; pick a
  naming convention.

## See also

- [Stage reference](../reference/stage.md): per-project operating modes stored on these entries.
- [CLI API reference](../reference/cli-api-reference.md): `ChdirResolver` and the `-C` flag.
