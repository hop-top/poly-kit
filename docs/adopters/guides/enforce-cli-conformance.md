# Enforce CLI conformance on an existing tool

> Migration steps for adopters running on `kit` < 0.1.0-alpha.0 (no
> enforcement) who need to flip `EnforceValidate=true`. The rules
> each step satisfies (S1-S2, H1-H10, C1-C4) are tabled in
> [`reference/conformance.md`](../reference/conformance.md#layer-a-static-checker).

## Who this is for

Adopters with a cobra tree built before the strict surface shipped.
New tools scaffolded by `kit init` start annotated.

## Step 1 — run the assertion in a unit test

```go
func TestCLIConforms(t *testing.T) {
    root := buildMyRoot()           // your normal constructor
    kitconformance.AssertCLI(t, root)
}
```

`AssertCLI` will dump every failing bucket. Start with the **hard
tier** (Pass 2 H1-H10). Skip Pass 2 C1-C4 for now — they're off by
default.

## Step 2 — address Pass 1 failures first (kit/side-effect, kit/idempotent)

Every leaf that fails `S1`/`S2` needs a `cli.SetSideEffect(cmd, ...)`
call. Pick the tier honestly:

- `SideEffectRead` — no state mutation, no network writes
- `SideEffectWrite` — disk writes, local state changes
- `SideEffectWriteExternal` — network writes outside the
  tool's own state (e.g. POSTing to a peer)
- `SideEffectDestructive` — irreversible deletes (data,
  credentials, ledgers)
- `SideEffectDestructiveExternal` — irreversible deletes affecting
  systems beyond the tool's own state

`SetIdempotency` defaults are auto-applied based on the verb name
(`list/show/get/info → yes`; `delete/destroy → no`; etc.), so you
mostly leave it untouched. Pin explicitly when the default is wrong.

## Step 3 — annotate Short + Long on every leaf

`H1` and `H2` are the loudest buckets. cobra's `Short` is the
one-line description shown in `--help`; `Long` is the paragraph
shown on `<cmd> --help`. Both must be non-empty.

```go
&cobra.Command{
    Use:   "deploy <env>",
    Short: "Deploy the current commit to the named environment",
    Long: "Build, package, and deploy the current commit to env. " +
        "Idempotent against the same SHA; --force re-deploys.",
    // ...
}
```

## Step 4 — register the reserved `status` subcommand

`H6`. The simplest path is `cli.WithStatus(cli.StatusConfig{})`:

```go
root := cli.New(cli.Config{...},
    cli.WithStatus(cli.StatusConfig{
        ExtraEnvKeys:   []string{"MYTOOL_*"}, // widen env filter
        RedactPatterns: []string{"PRIVATE"},  // your secret-key marker
    }),
)
```

That mounts a `<tool> status` subcommand with six default providers
(profile / env / workspace / auth / effective-config /
kit-annotations); extend via `root.RegisterStatusProvider(name, fn)`.

Adopters who already ship a `status` command of their own keep it —
H6 is a presence check, not a behavior check.

## Step 5 — shape annotations for non-canonical layouts

`H7-H10` cover the noun-verb shape rules. The canonical layout is
depth-2 (`<tool> <noun> <verb>`); deviations need explicit opt-ins.

| Layout | Annotation | Where to put it |
|--------|-----------|-----------------|
| `<tool> <verb>` (depth-1 leaf) | `kit/top-level-verb` | The leaf |
| `<tool> <noun> <sub-noun> <verb>` (depth ≥ 3) | `kit/hierarchical` | Every intermediate, **unless** the depth-1 ancestor is a reserved subcommand (status / spec) |

```go
// Depth-1 leaf: kit init, kit serve
cli.SetTopLevelVerb(initCmd)

// Depth-3+ tree: mytool foo bar baz
fooGroup := &cobra.Command{Use: "foo", Short: "Foo management"}
cli.SetHierarchical(fooGroup)
barGroup := &cobra.Command{Use: "bar", Short: "Bar management"}
cli.SetHierarchical(barGroup)
fooGroup.AddCommand(barGroup)
barGroup.AddCommand(bazLeaf)
```

If the tree genuinely exceeds depth 5, restructure — the cap is
hard-coded and represents a modeling smell. If a leaf at depth 3+
is unavoidable and the depth-1 ancestor is reserved (e.g. you have
`<tool> toolspec policy show`), no annotation is required.

## Step 6 — opt-in to configurable tiers when ready

```go
// Once your guidance authoring is caught up
cfg.EnforceGuidance = true

// Once your destructive surfaces have typed tokens
cfg.EnforceDestructiveToken = true

// Once your write/destructive leaves all carry rationales
cfg.EnforceDryRunRationale = true
```

Flip each independently. There's no requirement to flip all four at
once.

## Step 7 — the escape hatch

If you genuinely cannot annotate (legacy internal tool, vendored
cobra binary, etc.), keep `cli.Config{DisableValidate: true}` for
now. The flag is supported indefinitely; it exists for adopters who
opt out of the strict surface deliberately.

For commands kit ships internally that cannot reasonably carry the
full annotation set (compat shims, debug-only stubs), use
`cli.SetExemptValidation(cmd)` to opt out at the leaf level. This
is **reserved for kit-internal use** — adopter commands should
annotate instead.

## Related pages

- [`reference/conformance.md`](../reference/conformance.md): rule tables, `ValidationFailureMode`, error envelope, harness, scenario and story DSLs
- [`reference/cli-api-reference.md`](../reference/cli-api-reference.md): the Go CLI factory the setters hang off
- [`kit-init.md`](kit-init.md): scaffold a new tool that starts annotated
