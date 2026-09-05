# Folder README guide

Every directory an adopter or contributor can land in answers, in one
screen, what it is for and where to start. Two shapes, one lint
(`make lint-readmes`), and a one-line allowlist for deliberate exceptions.

## Which directories need one

A directory qualifies when any of these holds:

- it holds non-test source files directly (`.go`, `.ts`, `.py`, `.php`, `.rs`)
- it is a standalone tool: `tools/<name>`
- it is a template root: `templates/<name>` (unless it carries `README.md.tmpl`,
  which is rendered for the adopter at scaffold time)
- it is a contract set: a directory under `contracts/` holding files directly
- it is a docs section: a directory under `docs/` holding `.md` files directly
- it is an index: any walked directory with a qualifying directory below it
  (Shape A)

Excluded, with everything below them:

| Excluded | Why |
|----------|-----|
| `testdata`, `fixtures`, `fixture` | test inputs |
| `gen`, `*.pb.*`, `dist`, `target`, `build`, `vendor`, `node_modules`, `__pycache__`, `.venv`, `.pytest_cache` | generated or third-party output |
| `internal` | private API; the parent README covers it |
| `cmd` | entry points documented by the parent README |
| `test`, `tests`, `e2e`, `*_test` | test directories |
| hidden directories (`.git`, `.github`, ...) | tooling |
| `examples/<name>/**`, `templates/<name>/**` | one README at the payload root covers the tree |

Excluded, but their children are still walked:

- language source roots (`src/`, or a Python package next to `pyproject.toml`)
  when the parent directory already has a README

The exclusion rules are data at the top of
[`scripts/lint-readmes`](../../scripts/lint-readmes); change the lists, not
the logic.

## Shape A: index README

For a directory whose children are the point (`go/`, `go/core/`, `sdk/`,
`templates/`, `docs/` sections).

```markdown
# <name>

One sentence: what this directory groups and the question it answers.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`bus/`](bus/README.md) | in-process event bus | you need to publish or veto an event |

## Conventions

Rules that apply to every child (naming, layering, what may import what).
Omit the section when there are none.
```

Cap: 40 lines. The lint detects Shape A by the `## Contents` heading and
checks that every relative link in that section resolves.

## Shape B: package README

For an importable package, standalone tool, template root, contract set or
SDK module. The Go doc comment (or the language's equivalent) stays the API
reference; the README answers what `go doc` cannot.

```markdown
# <name>

## What it answers

One or two sentences: the question this package exists to answer, and the
rule of thumb for when it is the wrong package (name the neighbour).

## Use it when

- concrete situation → what you call
- concrete situation → what you call

## Quick start

Shortest working snippet, verified as printed. One snippet, not three.

## Contract

Only the parts a caller can be wrong about: invariants, wire formats,
cross-language parity obligations, exit codes. Link the contract doc
instead of restating it.

## Neighbours

Where the adjacent concerns live, by path, so nobody adds them here.

## See also

Adopter guide, ADR, contract fixture: links only.
```

Cap: 80 lines. Longer content moves to `docs/adopters` and is linked.

## Rules

- Audience: the adopter reading the tree. Design rationale goes to ADRs; the
  README links them.
- "What it answers" uses the same question framing as the security surface
  map in [`architecture/architecture.md`](architecture/architecture.md) so
  the two stay consistent.
- Every snippet is executed before it lands: `example_test.go` for Go, a doc
  test or scratch script for TS, Python, Rust and PHP.
- Cross-language mirrors: where a Go package has a port, the port's README
  uses the same question and neighbours and adds a "Parity" line pointing at
  the fixture row in [`contracts/parity`](../../contracts/parity/README.md).
- No task ids, no track names, no attribution anywhere in the repo.
- Caps are warnings in this release; they become failures once the tree is
  under cap.

## Lint

```sh
make lint-readmes
```

Checks, in order: allowlist and baseline are well formed; every qualifying
directory has `README.md`, an allowlist entry, or a baseline entry; every
relative link under a `## Contents` heading resolves; line counts stay under
the shape cap (WARN). Exit 1 on a missing README outside the baseline, a
broken Contents link, or a malformed allowlist line.

### Allowlist: deliberate exceptions

Add one line to [`.readme-allowlist`](../../.readme-allowlist), path first,
reason after `#`:

```text
go/core/xdg/scopetest   # test-only helper package, imported from _test.go files
```

The reason is mandatory and should say which README covers the directory or
why none ever will. The lint fails on an entry without a reason or pointing
at a missing directory, and warns when an entry grows a README.

### Baseline: known debt

[`.readme-baseline`](../../.readme-baseline) lists directories that qualify
today and still lack a README. Entries need no reason. When a README lands,
delete its line; a stale line only warns, so parallel branches can land
READMEs without touching the file. Regenerate the whole file only when
introducing the lint to a new tree:

```sh
scripts/lint-readmes --write-baseline
```
