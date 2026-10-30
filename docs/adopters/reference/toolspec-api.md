# ToolSpec API Reference

`hop.top/kit/go/ai/toolspec` -- structured knowledge about CLI tools.
Pure data types + BFS lookup. Sub-packages own I/O and deps.

> TS: `@hop-top/kit/toolspec` mirrors these types in camelCase
> (`loadToolSpec`, `validateToolSpec`, `findCommand`) and adds
> `toolSpecFromCommand(program, { annotations, schemaVersion,
> includeHidden })`, which projects a commander tree the way
> `WalkCobra` projects cobra (hidden commands omitted unless
> `includeHidden`, as `WithIncludeHidden`; hidden options always
> omitted; framework builtins `help`, `completion` and its shells,
> `man`, `__complete*` skipped, as `buildManifest` skips them;
> undashed flag names; root options on `flags`; a negated `--no-x` is
> a `no-x` flag of type `bool`, and `buildManifest` gives it default
> `false`, as pflag does; `default` and `choices` follow the manifest's
> rules, `variadic` is commander's, and `contract` is read off the
> command's own `kit/side-effect`, `kit/idempotent` and
> `kit/retryable`) and merges per-command annotations keyed by
> path (`"mission list"`). `exitCodes` comes from a `kit/exit-codes`
> entry on the command's `annotations` property. `buildManifest(program,
> schemaVersion, { includeDeprecated, reserved })` and
> `renderManifestJSON` reproduce `BuildManifest` and its JSON bytes from
> the same kit/* annotations (summary/description map to short/long;
> runnable = has an action); `negotiateSchemaVersion` mirrors
> `NegotiateSchemaVersion`. `registerSpecCommand(program, schemaVersion,
> { reserved })` mounts `spec` and `spec coverage` with Go's flags and
> bytes; their output schemas come from `contracts/toolspec-spec-v1/`.
> Python loads YAML only: `hop_top_kit.toolspec.load_toolspec` maps
> every field below (YAML keys are the snake_case json tags, e.g.
> `undeclared_args`, `exit_codes`) into dataclasses of the same
> names, ignores unknown keys, and has no command-tree projection.

## Schema Overview

```
ToolSpec
  +-- name, schema_version
  +-- commands[]         -> Command tree (recursive children)
  +-- flags[]            -> Global flags
  +-- error_patterns[]   -> Known errors + fixes
  +-- workflows[]        -> Multi-step sequences
  +-- state_introspection -> Config/env/auth discovery
```

## ToolSpec

```go
type ToolSpec struct {
    Name               string
    SchemaVersion      string
    Commands           []Command
    Flags              []Flag
    ErrorPatterns      []ErrorPattern
    Workflows          []Workflow
    StateIntrospection *StateIntrospection
}
```

## Command

Recursive tree node. Supports nested subcommands via `Children`.

```go
type Command struct {
    Name            string
    Short           string         // one-line summary
    Aliases         []string
    Flags           []Flag
    Args            []Arg          // kit/args, in order: {Name, Required, Variadic}
    UndeclaredArgs  bool           // usage line names operands kit/args does not
    ExitCodes       []string       // kit/exit-codes symbols (exit-taxonomy-v1)
    Children        []Command
    Contract        *Contract
    Safety          *Safety
    PreviewModes    []string       // "dryrun", "plan", "diff"
    OutputSchema    *OutputSchema
    Intent          *Intent
    SuggestedNext   []string
    Deprecated      bool
    DeprecatedSince string
    ReplacedBy      string
}
```

### Positional arguments (`kit/args`)

Cobra does not introspect argument names, so a command names its
operands, in order, in the `kit/args` annotation. One grammar serves
every reader (`cmdmeta.ParseArgs`: the ToolSpec, the manifest, MCP,
REST and shell completion):

| Entry      | `Required` | `Variadic` | Meaning                          |
|------------|------------|------------|----------------------------------|
| `name`     | true       | false      | exactly one operand              |
| `name?`    | false      | false      | zero or one                      |
| `name...`  | true       | true       | one or more, every remaining one |
| `name...?` | false      | true       | zero or more, every remaining one|

Entries are comma-separated, whitespace trimmed. The dots come before
the `?`; any other suffix is part of the name. A variadic argument
must be declared last; kit does not check that, the command's own
`Args` validator still decides what it accepts.

```go
Annotations: map[string]string{"kit/args": "name, note?, files...?"},
```

## Contract

Behavioural guarantees of a command.

| Field             | Type       | Description                                           |
|-------------------|------------|-------------------------------------------------------|
| `Idempotent`      | `bool`     | True iff `kit/idempotent` is `yes`                    |
| `IdempotentClass` | `string`   | The raw `kit/idempotent` class: `yes`, `no`, `conditional`; empty when undeclared (json `idempotent_class`) |
| `SideEffects`     | `[]string` | e.g. `["destructive"]`                                |
| `Retryable`       | `bool`     | `kit/retryable`                                       |
| `PreConditions`   | `[]string` | Must hold before execution                            |

`IdempotentClass` exists because the bool cannot tell `no` from
`conditional` from "nothing declared". `conditional` matters to an
agent: kit registers `--idempotency-key` on such a command, and a
retry must supply one.

## Safety

Risk metadata for a command.

| Field                  | Type          | Description               |
|------------------------|---------------|---------------------------|
| `Level`                | `SafetyLevel` | `safe`, `caution`, `dangerous` |
| `RequiresConfirmation` | `bool`        | Has `--yes`/`--force`/`-y`|
| `Permissions`          | `[]string`    | Required permissions      |

### Permission tokens

`Permissions` carries the tokens declared in
[`go/ai/toolspec/permissions.go`](../../../go/ai/toolspec/permissions.go).
The filesystem tier is derived from a command's declared `kit/fs` value.

| Value                   | `Safety.Level` | Permission                   |
|-------------------------|----------------|------------------------------|
| `read`                  | safe           | `kit:fs:read`                |
| `write-local`           | caution        | `kit:fs:write:local`         |
| `write-shared`          | caution        | `kit:fs:write:shared`        |
| `destructive-local`     | dangerous      | `kit:fs:destructive:local`   |
| `destructive-shared`    | dangerous      | `kit:fs:destructive:shared`  |
| `interactive`           | caution        | `kit:fs:read`                |
| `write` (legacy)        | caution        | `kit:fs:write:shared`        |
| `destructive` (legacy)  | dangerous      | `kit:fs:destructive:shared`  |

Legacy values map conservatively: bare `write` and `destructive` assume
shared scope. Adopters who mean `write-local` or `destructive-local`
must declare the value explicitly.

`kit/network` is an orthogonal axis, default `none`, independent of the
tier: a `read` can be `egress:public`, a `destructive-local` can be
`none`.

| Value              | Permission                    |
|--------------------|-------------------------------|
| (absent or `none`) | `kit:network:none`            |
| `egress:public`    | `kit:network:egress:public`   |
| `egress:private`   | `kit:network:egress:private`  |
| `ingress`          | `kit:network:ingress`         |

Subprocess execution and bus publication carry `kit:exec:subprocess`
and `kit:bus:publish`.

### Safety vocabulary

`cmdreflect` projects three orthogonal axes from a command's cobra
annotations into `Safety.Level`, `Safety.RequiresConfirmation` and the
typed `Safety.Permissions` slice. Reflection happens once, in
[`cmdreflect`](cmdreflect.md); the reasons a command is reflected but
withheld from the spec (`hidden-internal`, `deprecated`, `interactive`,
`management-only`, `malformed-schema`, …) are the
[non-invocable reasons](cmdreflect.md#non-invocable-reasons) defined
there, not here.

`RequiresConfirmation` is `true` when any of these hold:

- the resolved tier is destructive (`destructive-local`,
  `destructive-shared`, legacy `destructive`)
- `kit/network` is `egress:private` or `ingress`, regardless of tier
- the destructive-name heuristic inferred the tier
- the adopter declared it explicitly (see
  [two confirmation fields](cmdreflect.md#two-confirmation-fields))

Two capability annotations add a permission token only when present:

| Annotation        | Permission            | Meaning                              |
|-------------------|-----------------------|--------------------------------------|
| `kit/exec`        | `kit:exec:subprocess` | Spawns a subprocess                  |
| `kit/bus-publish` | `kit:bus:publish`     | Publishes events to the kit event bus |

#### Default policy table

The harness-side decoder is the `policy` package
([`go/ai/toolspec/policy/default.yaml`](../../../go/ai/toolspec/policy/default.yaml)
is the shipped table; a `--policy <file>` overlay merges over it, overlay
rules winning on collisions). It keys on the four side-effect classes and
a three-value network axis:

| Side effect × Network | `none`     | `local-only` | `egress` |
|-----------------------|------------|--------------|----------|
| `read`                | auto-allow | auto-allow   | prompt   |
| `write`               | auto-allow | prompt       | prompt   |
| `destructive`         | prompt     | prompt       | deny     |
| `interactive`         | prompt     | prompt       | prompt   |

Resolution rules, as implemented by `Table.Resolve`:

- Exact `(side_effect, network)` match first, then the side effect's
  `any` rule; anything else falls to a fail-safe `prompt` attributed to
  `fallback`.
- The manifest's `side_effect` carries what you wrote, not kit's
  normalisation of it. A six-tier value such as `write-local` has no
  rule in the shipped table today and therefore resolves to the
  fail-safe prompt; the resolved tier reaches harnesses through the
  permission tokens above instead.
- A leaf with no `kit/side-effect` annotation is treated as
  `destructive`, so unannotated commands fail safe.
- The network axis is read from `kit/network` when present and
  defaults to `none` otherwise; the decision reason notes when it
  defaulted.

## Intent

Classifies command purpose for AI routing.

| Field      | Type       | Description                |
|------------|------------|----------------------------|
| `Domain`   | `string`   | e.g. `"deployment"`        |
| `Category` | `string`   | e.g. `"create"`            |
| `Tags`     | `[]string` | Free-form classification   |

## OutputSchema

Expected output format of a command.

| Field     | Type       | Description               |
|-----------|------------|---------------------------|
| `Format`  | `string`   | e.g. `"json"`, `"table"`  |
| `Fields`  | `[]string` | Known output fields       |
| `Example` | `string`   | Sample output             |

## StateIntrospection

Commands/vars for discovering tool state.

```go
type StateIntrospection struct {
    ConfigCommands []string  // e.g. ["git config --list"]
    EnvVars        []string  // e.g. ["GIT_DIR", "GIT_WORK_TREE"]
    AuthCommands   []string  // e.g. ["gh auth status"]
}
```

## Flag

```go
type Flag struct {
    Name        string   // e.g. "--verbose"
    Short       string   // e.g. "-v"
    Type        string   // e.g. "bool", "string"
    Description string
    Required    bool     // MarkFlagRequired / makeOptionMandatory
    Deprecated  bool
    ReplacedBy  string
    Default     string   // pflag DefValue: "false", "[a,b]", ""; same bytes as the manifest's default
    Choices     []string // WithFlagEnum / .choices(), in order
}
```

`Default` follows exactly the manifest's rule (pflag's `DefValue`,
omitted when empty), so a ToolSpec and a manifest built from the same
tree never disagree on a default: a bool is `"false"`, a list `"[a,b]"`
or `"[]"`.

`Choices` is the set declared once with
[`Root.WithFlagEnum`](flag-enums.md#flag-value-enums). The same set
also reaches `Description` as the `(one of: …)` suffix help renders,
because the suffix is stamped onto the flag's usage string at
`Execute`; the duplication is accepted so help output and existing
descriptions stay byte-stable. Read `Choices`, not the suffix.

## Manifest schema versions

The `<tool> spec` manifest (`toolspec.Manifest`) is versioned
separately from this ToolSpec shape; see
[the harness guide](../integrations/toolspec-harness-guide.md#step-1--discover-the-manifest)
for negotiation. Every version is additive over the one before:

| `schema_version` | Adds |
|------------------|------|
| `1.0` | the original leaf layout |
| `1.1` | per-command fields surfacing kit's command annotations (`long`, `retryable`, `output_schema`, `examples`, `next_steps`, …) |
| `1.2` | per-flag `required` and `choices`, per-argument `variadic` |

The ToolSpec fields above (`Default`, `Choices`, `Variadic`,
`IdempotentClass`) carry no version: `schema_version` on a ToolSpec is
a free-form label, and every addition is `omitempty`.

## ErrorPattern

Maps known error output to actionable fixes.

```go
type ErrorPattern struct {
    Pattern    string      // regex or substring
    Fix        string      // primary fix suggestion
    Source     string      // e.g. "help", "thefuck", "llm"
    Cause      string      // root cause category
    Fixes      []string    // alternative fixes
    Confidence float32     // 0.0-1.0
    Provenance *Provenance
}
```

## Workflow

Common multi-step sequence for a tool.

```go
type Workflow struct {
    Name       string
    Steps      []string            // ordered commands
    After      map[string][]string // suggested follow-ups
    Provenance *Provenance
}
```

## Provenance

Tracks where spec data originated.

| Field         | Type      | Description                  |
|---------------|-----------|------------------------------|
| `Source`       | `string`  | `"help"`, `"llm"`, etc.     |
| `RetrievedAt` | `string`  | RFC3339 timestamp            |
| `Confidence`  | `float32` | Reliability score (0.0-1.0)  |

## FindCommand

BFS lookup in the command tree:

```go
cmd := spec.FindCommand("deploy")
// returns *Command or nil
```

Searches breadth-first; returns shallowest match by `Name`.

## Sources

Sources implement the `Source` interface:

```go
type Source interface {
    Resolve(tool string) (*ToolSpec, error)
}
```

`SourceFunc` adapts plain functions:

```go
src := toolspec.SourceFunc(func(tool string) (*ToolSpec, error) {
    // ...
})
```

### Built-in Sources

| Package                      | Description                     |
|------------------------------|---------------------------------|
| `toolspec/sources/help`      | Parses `--help` output          |
| `toolspec/sources/completion`| Parses zsh/bash completion      |
| `toolspec/sources/tldr`      | Parses tldr pages               |
| `toolspec/sources/thefuck`   | Extracts error patterns         |
| `toolspec/sources/llm`       | LLM-generated patterns/intent   |
| `toolspec/sources/usp`       | User shell patterns (history)   |

### ChainSources

Query multiple sources in order; merge results:

```go
src := toolspec.ChainSources(helpSrc, tldrSrc, llmSrc)
spec, err := src.Resolve("docker")
```

Earlier sources take precedence; later sources fill empty fields.

## Registry

Higher-level resolver with optional caching:

```go
reg := toolspec.NewRegistry(
    toolspec.WithSource(&help.HelpSource{}),
    toolspec.WithSource(llm.NewLLMSource(cfg)),
    toolspec.WithCache(sqliteCache),
)
spec, err := reg.Resolve("git")
```

Resolution: cache check -> query sources in order -> merge ->
cache result.

## Merge & Diff

```go
merged := toolspec.Merge(base, overlay)
delta  := toolspec.Diff(a, b)
```

- `Merge`: overlay fills empty fields in base (deep copy)
- `Diff`: returns fields present in b but missing in a

Slice fields (commands, flags, errors, workflows) are
all-or-nothing: overlay slice used only when base slice is empty.

## Help Source Details

`sources/help.ParseHelpOutput(name, output)` handles:
- Standard `Commands:` / `Flags:` sections
- ALL-CAPS headers (gh, wrangler style)
- git-style narrative preambles
- Automatic inference of Contract, Safety, PreviewModes

Heuristics:
- Destructive names (`delete`, `rm`, `destroy`...) get
  `Safety.Level = dangerous` and `Contract.SideEffects`
- `--yes`/`--force`/`-y` flags set `RequiresConfirmation`
- `--dry-run`/`--plan`/`--diff` flags populate `PreviewModes`

## 12-Factor CLI Alignment

ToolSpec supports the 12-factor CLI principles:

1. **Discoverability** -- command tree + intent tags
2. **Safety classification** -- safe/caution/dangerous levels
3. **Idempotency contracts** -- explicit retryable/idempotent
4. **Preview modes** -- dry-run, plan, diff
5. **Error recovery** -- patterns with fixes + confidence
6. **State introspection** -- config/env/auth commands
7. **Structured output** -- format + fields + example
8. **Workflow guidance** -- common multi-step sequences
9. **Deprecation tracking** -- deprecated + replaced_by
10. **Provenance** -- source + confidence + timestamp

## Writing a .toolspec.yaml

Manual spec files follow the same JSON schema. Example:

```yaml
name: myctl
schema_version: "1"
state_introspection:
  config_commands: ["myctl config list"]
  env_vars: ["MYCTL_TOKEN", "MYCTL_ENDPOINT"]
  auth_commands: ["myctl auth status"]
commands:
  - name: deploy
    contract:
      idempotent: true
      retryable: true
    safety:
      level: caution
      requires_confirmation: true
    preview_modes: ["dryrun"]
    output_schema:
      format: json
    intent:
      domain: deployment
      category: create
      tags: [infrastructure, cloud]
    children:
      - name: rollback
        safety:
          level: dangerous
          requires_confirmation: true
        contract:
          side_effects: ["destructive"]
  - name: status
    contract:
      idempotent: true
    safety:
      level: safe
    output_schema:
      format: table
      fields: [name, status, replicas]
```

Load via `sources/help` or any custom `Source` that reads YAML
and unmarshals into `ToolSpec`.
