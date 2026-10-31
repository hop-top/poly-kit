# Enable flag autocorrect

> Audience: Go adopters shipping a kit CLI, and the operators and agents who run it. Covers what
> a mistyped flag or command does by default, how to opt in to automatic correction, and what an
> adopter must declare so correction stays safe. Normative detail lives in the
> [flag enums reference](../reference/flag-enums.md#acting-on-the-correction).

## What you get without doing anything

Every CLI built on `cli.New` turns a mistyped flag into a structured `USAGE` error that names
the fix. Nothing is applied; the process fails and the caller retries.

```console
$ demo show --nam x
USAGE: unknown flag --nam
Cause: no flag named --nam on demo show
Fix: --name
```

The error is the same `output.Error` envelope the rest of kit uses, so `--format json|yaml`
callers receive it as data. Unknown commands get cobra's own suggestion, also suggest-only:

```console
$ demo shw
USAGE: unknown command "shw" for "demo"

Did you mean this?
    show
```

Autocorrect (applying the fix) covers flag names only. A mistyped command is never rewritten.

## 1. Turn it on (operator or agent side)

`cli.autocorrect` has three values: `off` (default), `prompt` and `read`. Set it at any of three
rungs; the highest wins:

| Rung | How |
|---|---|
| flag | `demo --autocorrect=read show --nam x` |
| environment | `KIT_AUTOCORRECT=read demo show --nam x` |
| config file | key `cli.autocorrect`, for example `autocorrect: read` under `cli:` |

An explicit `--autocorrect=off` beats `KIT_AUTOCORRECT=read`, so a script can opt one call back
out. An unrecognised value at a rung falls through to the next; it never fails the run. The flag
is hidden from `--help` and listed under `--help-all`.

With `read`, a correctable typo on a read-only command runs, and the correction is reported on
stderr:

```console
$ demo --autocorrect=read show --nam x
OK: applied flag correction: --nam -> --name
Corrected from: --nam
Fix: --name
name = x
```

With `--format json` the same notice is a JSON envelope carrying `"corrected_from": "--nam"`, so
an audit log records what was typed and what ran. The field is absent when nothing was
corrected.

`prompt` asks `Did you mean --name instead of --nam? [Y/n]` on a terminal, reading `/dev/tty`
rather than stdin so piped input is not consumed. With no terminal it behaves like `off` and
never blocks.

## 2. Declare side-effects so `read` can act

`read` applies a correction only when the resolved leaf is tagged `kit/side-effect: read`.
Write, destructive and interactive leaves, and any leaf with no tag, stay suggest-only. An
untagged leaf is not assumed safe. The adopter's job is therefore the tag you already owe
`Root.Validate`:

```go
show := &cobra.Command{Use: "show", Short: "Show a thing", RunE: runShow}
cli.SetSideEffect(show, cli.SideEffectRead)

del := &cobra.Command{Use: "delete", Short: "Delete a thing", RunE: runDelete}
cli.SetSideEffect(del, cli.SideEffectDestructive)
```

Run against both with `--autocorrect=read`, `show --nam` is applied and `delete --forc` is
refused with the usual suggestion. `prompt` may ask about any leaf, but its default answer
follows the same tag: Enter accepts on a read, a mutating leaf needs an explicit `y`.

## 3. What counts as correctable

Correction needs more certainty than suggestion. All must hold:

- the typed name is at least 3 characters;
- exactly one candidate matches;
- that candidate is within 1 edit, or the typed name is a unique prefix of it.

Suggestion allows 2 edits, so some typos are suggested but never applied. A transposition such
as `--nmae` is two edits: it yields a `Fix`, not a rewrite. Shorthand groups (`-xyz`) and bad
flag values are never corrected, and anything after a bare `--` is left alone.

## 4. Gates still see the corrected flags

A rewrite is re-dispatched through the normal path: flag parsing, validators, the confirm gate,
dry-run and idempotency middleware. `--dry-run` therefore applies to the corrected `--name`, not
the typo. Only one rewrite happens per invocation; a second unknown flag fails normally.

`--autocorrect` is separate from `--confirm`. `--confirm=yes` never implies autocorrect.

## Checking your tool

Run a typo through the built binary, because the behaviour lives in `Execute`, not in a
single function:

```console
$ demo show --nam x                       # suggest only, non-zero exit
$ demo --autocorrect=read show --nam x    # applied, corrected_from reported
$ demo --autocorrect=read delete --forc   # destructive: still suggest only
```

The repository's own end-to-end cases are in `go/console/cli/flagerrexit/`; copy that
build-and-run pattern for your tool.

## See also

- [flag enums reference](../reference/flag-enums.md): `WithFlagEnum`, the full error format
  and the correction rules.
- [side-effect reference](../reference/sideeffect.md): what each tier gates.
- [declare and read command annotations](declare-and-read-command-annotations.md)
