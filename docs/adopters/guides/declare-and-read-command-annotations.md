# Declare and read command annotations

> Audience: Go adopters who (a) tag their cobra commands so kit can gate, document and serve
> them, or (b) write a transport, doc generator or reflection layer that must read those tags.
> Packages: `hop.top/kit/go/console/cli` (setters) and `hop.top/kit/go/console/cli/cmdmeta`
> (readers).

kit keeps per-command metadata in cobra's `Annotations` map, under the reserved `kit/` prefix.
Two packages split the work:

| Package | Role | Imports |
|---|---|---|
| `cli` | declare: `SetSideEffect`, `SetRetryable`, `SetGuidance`, `SetOutputSchema`, ... | the whole CLI framework |
| `cmdmeta` | read: `Is*`, `Get*`, `ParseArgs`, `Key*` constants | cobra and the standard library only |

Every `cmdmeta` reader is also reachable as `cli.IsRetryable`, `cli.GetExamples`, and so on; `cli`
forwards. Use `cmdmeta` when you must not link `cli`: it exists so that reflection code
(`hop.top/kit/go/ai/cmdreflect`) and transports can read a tree without an import cycle. Package
README: [cmdmeta](../../../go/console/cli/cmdmeta/README.md).

## 1. Declare annotations on a command

```go
cmd := &cobra.Command{
    Use:   "deploy <target>",
    Short: "Roll out a release",
    Long:  "Roll a release out to a target environment.",
    Args:  cobra.ExactArgs(1),
    RunE:  run,
}
cli.SetSideEffect(cmd, cli.SideEffectWrite)
cli.SetRetryable(cmd, true)
cmd.Annotations[cmdmeta.KeyArgs] = "target"

err := cli.SetGuidance(cmd, cli.Guidance{
    Examples: []cli.Example{
        {Title: "Deploy to staging", Command: "tool deploy staging"},
    },
    NextSteps: []cli.NextStep{
        {When: "on success", Suggest: "tool status", Reason: "confirm rollout"},
    },
})
```

`cmd.Annotations` is nil on a fresh command. The `cli.Set*` helpers allocate the map; when you
write a key directly, as with `KeyArgs` above, call a setter first or make the map yourself.

Setters worth knowing, by what they declare:

| Declare | Setter | Key |
|---|---|---|
| side-effect tier (`read`, `write`, `write-local`, `write-shared`, `destructive`, `destructive-local`, `destructive-shared`, `interactive`) | `cli.SetSideEffect` | `kit/side-effect` |
| safe to re-run | `cli.SetRetryable` | `kit/retryable` |
| examples and follow-up steps | `cli.SetExamples`, `cli.SetNextSteps`, `cli.SetGuidance` | `kit/examples`, `kit/next-steps` |
| JSON Schema of the result | `cli.SetOutputSchema(cmd, cli.OutputSchema{Type, Version, Example})` | `kit/output-schema`, `kit/output-schema-version` |
| tree shape | `cli.SetTopLevelVerb`, `cli.SetHierarchical`, `cli.SetPassthrough` | `kit/top-level-verb`, `kit/hierarchical`, `kit/passthrough` |
| served-result cache lifetime (read leaves only) | `cli.SetCacheTTL` | `kit/cache-ttl` |
| kept off named served surfaces | `cli.SetWithholdFrom` | `kit/withhold-from` |
| dry-run opt-out | `cli.OptOutDryRun`, `cli.SetDryRunRationale` | `kit/dry-run`, `kit/dry-run-rationale` |
| deprecation | `cli.SetDeprecatedSince`, `cli.SetRemovalTarget` | `kit/deprecated-since`, `kit/removal-target` |
| positional operands | write `cmdmeta.KeyArgs` directly | `kit/args` |

Notes that save a debugging session:

- `SetOutputSchema` needs a non-empty `Version` (`MAJOR.MINOR`) whenever `Type` is set, and
  returns an error otherwise. A nil `Type` clears both keys.
- `SetExamples` and `SetNextSteps` marshal to JSON and fail when the payload exceeds 16384
  bytes. `SetExamplesWithCap` and `SetNextStepsWithCap` take an explicit cap. An empty slice
  clears the key.
- `SetCacheTTL` writes `ttl.String()`. `Root.Validate` rejects the tree if the value is not a
  duration greater than zero, or if the command is not declared `read`.
- `Root.Validate` also enforces the side-effect tier on every leaf; see the
  [side-effect reference](../reference/sideeffect.md) for what each tier gates.

## 2. Read annotations

```go
func describe(cmd *cobra.Command) {
    se, _ := cli.GetSideEffect(cmd)
    fmt.Printf("%s: side-effect=%q retryable=%t dry-run=%t reserved=%t\n",
        cmd.CommandPath(), se, cmdmeta.IsRetryable(cmd),
        cmdmeta.IsDryRunSupported(cmd), cmdmeta.InReservedTree(cmd))

    for _, a := range cmdmeta.ParseArgs(cmd.Annotations[cmdmeta.KeyArgs]) {
        fmt.Printf("  arg %s optional=%t variadic=%t\n", a.Name, a.Optional, a.Variadic)
    }
    if steps, ok := cmdmeta.GetNextSteps(cmd); ok {
        for _, s := range steps {
            fmt.Printf("  next: %s (%s)\n", s.Suggest, s.Reason)
        }
    }
    if ttl, ok := cmd.Annotations[cmdmeta.KeyCacheTTL]; ok {
        d, err := cmdmeta.ParseCacheTTL(ttl)
        fmt.Println("  cache-ttl:", d, err)
    }
}
```

For the `deploy` command above this prints:

```text
tool deploy: side-effect="write" retryable=true dry-run=true reserved=false
  arg target optional=false variadic=false
  next: tool status (confirm rollout)
```

Reader behaviour to rely on:

- Boolean readers (`IsTopLevelVerb`, `IsHierarchical`, `IsPassthrough`, `IsRetryable`,
  `IsSelfHosting`, `ReadBool`) are true only for the exact string `"true"`. A nil command or nil
  map is false.
- `GetExamples` and `GetNextSteps` return `(nil, false)` for an absent or malformed value.
  Decode errors are swallowed on purpose; unmarshal the raw annotation yourself if you need
  them.
- `GetOutputSchemaJSON` returns `(raw, version, ok)`. It does not validate the schema bytes.
- `IsDryRunSupported` resolves in this order: `kit/dry-run: opted-out` is false;
  `kit/dry-run: supported` is true; a `write*` or `destructive*` side-effect is true; `read`,
  `interactive`, an unknown value or no tag is false. A command with no tag is therefore never
  reported as dry-run capable.
- `InReservedTree` is true for a command marked `kit/reserved` and everything beneath it.
  Reserved trees are kit's own management commands (`alias`, `uri`, `stage`, and others); no
  served surface runs them. Do not set `kit/reserved` on your own commands; use
  `cli.SetWithholdFrom` to keep a command off particular surfaces.

## 3. The `kit/args` grammar

Cobra does not know operand names, so `kit/args` carries them: a comma list, one entry per
operand, in order.

| Entry | Meaning |
|---|---|
| `name` | required |
| `name?` | optional |
| `name...` | variadic, one or more |
| `name...?` | variadic, zero or more |

`cmdmeta.ParseArgs("src, dst?, files...?")` yields three `ArgSpec` values (`Name`, `Optional`,
`Variadic`). Declare a variadic last; the parser does not check that, your command's `Args`
validator does. The manifest, the MCP and REST projections and shell completion all read the
same grammar through this function, so use it rather than splitting the string yourself.

## 4. Write your own reader

For a `kit/*` key without a typed helper, read the string through the constants, never a string
literal, so a rename cannot drift:

```go
if cmdmeta.ReadBool(cmd, cmdmeta.KeyPassthrough) { /* ... */ }
raw := cmd.Annotations[cmdmeta.KeyExitCodes] // comma-separated exit-code symbols
```

The full key set is the `Key*` constants in `go/console/cli/cmdmeta/annotations.go` plus
`KeyArgs`.

## See also

- [side-effect reference](../reference/sideeffect.md): tiers, dry-run and the confirm gate.
- [cmdreflect reference](../reference/cmdreflect.md): the main consumer of these readers.
- [cmdsurface reference](../reference/cmdsurface.md): which annotations each served surface
  honours.
