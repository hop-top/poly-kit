# cli

standardized CLI framework and flag parsing.

## Flag validators

`Root.WithFlagValidator(name, fn)` registers a closure that rejects ill-formed
values for a persistent flag and routes the rejection through kit's structured
`output.RenderError` envelope (honoring `--format json|yaml|table|text`).

```go
root.WithFlagValidator("api-version", func(v string) *output.Error {
    if !semver.IsValid(v) {
        return &output.Error{
            Code:     "INVALID_API_VERSION",
            Message:  "api-version must be semver",
            ExitCode: 2,
        }
    }
    return nil
})
root.WrapRunE() // installs the validator on every leaf
```

This replaces the hand-rolled tree-walking pattern (e.g. an `installAPIVersionGuard`
that wraps each leaf's RunE manually). The middleware:

- runs once per leaf invocation, AFTER cobra parses the flag and BEFORE the adopter RunE
- only fires when the user actually set the flag (`flag.Changed == true`); defaults pass through
- last-registered wins for a given name (ergonomic for tests)
- silently never fires when the named flag doesn't exist anywhere on the tree (no panic)

Ordering: call `WithFlagValidator` BEFORE `WrapRunE` (or before `Execute`, which calls
`WrapRunE`). Validators registered after the subtree is wrapped are inert.

## Flag value enums

`Root.WithFlagEnum(name, values...)` declares the closed set of legal values for
a flag once. Three consumers read that single declaration:

```go
root.WithFlagEnum("status", "TODO", "IN_PROGRESS", "DONE", "SKIPPED")
```

| consumer | effect |
|---|---|
| parse-time errors | `--status` with no value renders the whole set as `alternatives` |
| `--help` | the usage line gains `(one of: TODO, IN_PROGRESS, DONE, SKIPPED)` |
| shell completion | the shell offers exactly those values |

`WithFlagEnum` is **tree-wide**: every flag of that name anywhere in the tree
gets the set. That is right for a flag whose meaning is global — a root
persistent `--format`, a `--status` the whole tool shares.

When two commands register the same flag NAME with different legal sets, scope
each declaration with `WithCommandFlagEnum(path, name, values...)`:

```go
root.WithCommandFlagEnum("list",   "type", "TASK", "TRACK")
root.WithCommandFlagEnum("export", "type", "json", "csv", "md")
```

`path` is the command path below the root, space-separated (`"widget list"`).
Only that command's flag is stamped, so `list --type` and `export --type` keep
their own sets in errors, help, and completion. A scoped declaration wins over a
tree-wide one for the same flag; a path naming no command stamps nothing.

`Root.FlagEnum(name)` reads back a tree-wide declaration,
`Root.CommandFlagEnum(path, name)` a scoped one.

Declaring an enum is metadata, not validation — it does not reject anything on
its own. Pair it with `WithFlagValidator` when a wrong value must actually fail.

Ordering: call `WithFlagEnum` BEFORE `Execute`, which stamps the values onto the
matching flags. Declaring before the flag itself is registered is fine; the tree
is walked at `Execute` time.

Storage is the `cli.FlagEnumAnnotation` pflag annotation on the flag itself, and
all three consumers read only that. An adopter that sets the annotation directly
— on a generated flag, say — gets errors, help, and completion the same as one
using the registry.

A flag that already has a completion function registered keeps it — an
adopter-supplied dynamic completer beats the static list.

## Parse-time flag errors

A bad flag fails during cobra's parse, before any `RunE` runs, so neither the
flag validators nor the error-envelope middleware can see it. Kit hooks the
root's `FlagErrorFunc` — the same seam that classifies a malformed invocation as
`USAGE` — and turns pflag's typed errors into the `output.Error` envelope
everything else uses, so `--format json|yaml` callers get structure rather than
prose:

```console
$ tool list --count
USAGE: unknown flag --count
Cause: no flag named --count on tool list
Fix: --counters

$ tool list --zzz
USAGE: unknown flag --zzz
Cause: no flag named --zzz on tool list
Fix: run 'tool list --help' for usage

$ tool list --status
USAGE: missing value for --status
Cause: --status requires a value
Fix: --status TODO
Alternative: --status TODO
Alternative: --status IN_PROGRESS
Alternative: --status DONE
Alternative: --status SKIPPED
```

An unknown flag is matched against the flag set `--help` would show for that
command by prefix first (`--count` → `--counters` is an abbreviation, not a
typo), then by Levenshtein distance within 2 edits. A single unambiguous hit
becomes `Fix`; a tie becomes `Alternatives` with `Fix` pointing at `--help`; no
match falls back to `--help` alone.

Two refusals to guess, both deliberate:

- A shorthand group (`-xyz`) carries no name to match against.
- A typed name under 3 characters is within 2 edits of most of a real flag
  table, so its best candidate is demoted to `Alternatives` and `Fix` points at
  `--help`. `--ax` offers `--ab`; it does not assert it.

Candidates are what leaf help advertises, which includes the kit-owned globals
(`--dry-run`, `--confirm`, `--chdir`, `--config`) that root `--help` suppresses
for cross-language parity. A flag hidden everywhere is never suggested.

A suggestion is never applied. The correction is offered and the process exits
non-zero (`USAGE`, exit 2) — the same path serves destructive verbs, where
silently running a guessed `--force` costs more than the round trip it saves.

The envelope retains the original pflag error, so `errors.As` still matches
`*pflag.NotExistError` / `*pflag.ValueRequiredError`, and `*output.Error` for
adopters reading `ExitCode` in `main`.

One envelope, on every driver. The refusal is written at cobra's own seam, not
in fang's error handler, so `Root.Prepare` + `Cmd.ExecuteContext`, the
in-process runner behind the served surfaces, and the conformance harness all
get the same stderr `Execute` does — and get it exactly once.

An adopter `FlagErrorFunc` still runs first and still owns what it returns: an
`*output.Error` it built passes through with its code, exit code and fix intact;
a bare error it returns (pflag's own, unchanged) gets kit's classification and
suggestion.

## Sub-packages

| Path | What it answers |
|------|-----------------|
| [`breaker/`](breaker/README.md) | `breaker` subcommand tree: which breakers are registered, their state, closing one |
| [`cmdmeta/`](cmdmeta/README.md) | read the `kit/*` annotations on a cobra command without importing `cli` |
| [`completion/`](completion/README.md) | dynamic shell completions for flags and positionals |
| [`config/`](config/README.md) | `config path` and `config paths` subcommands: which file loads and the precedence chain |
| [`conformance/`](conformance/README.md) | the `kit conformance` command tree and its exit codes |
| [`idemstore/`](idemstore/README.md) | storage backend for `--idempotency-key` recorded results |
| [`policy/`](policy/README.md) | delegation-safety policy YAML enforced per agent-driven invocation |
| [`router/`](router/README.md) | `kit llm router` subtree: start, stop, list, inspect RouteLLM instances |
| [`scope/`](scope/README.md) | `kit scope show`, `check`, `test`: would the path policy allow this path |
