# ToolSpec API Reference

`hop.top/kit/go/ai/toolspec` -- structured knowledge about CLI tools.
Pure data types + BFS lookup. Sub-packages own I/O and deps.

> Go-only. No TS/Python bindings yet.

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
    Aliases         []string
    Flags           []Flag
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

## Contract

Behavioural guarantees of a command.

| Field          | Type       | Description                     |
|----------------|------------|---------------------------------|
| `Idempotent`   | `bool`     | Safe to re-run                  |
| `SideEffects`  | `[]string` | e.g. `["destructive"]`          |
| `Retryable`    | `bool`     | Has `--force` or `--dry-run`    |
| `PreConditions`| `[]string` | Must hold before execution      |

## Safety

Risk metadata for a command.

| Field                  | Type          | Description               |
|------------------------|---------------|---------------------------|
| `Level`                | `SafetyLevel` | `safe`, `caution`, `dangerous` |
| `RequiresConfirmation` | `bool`        | Has `--yes`/`--force`/`-y`|
| `Permissions`          | `[]string`    | Required permissions      |

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
    Name        string  // e.g. "--verbose"
    Short       string  // e.g. "-v"
    Type        string  // e.g. "bool", "string"
    Description string
    Deprecated  bool
    ReplacedBy  string
}
```

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
