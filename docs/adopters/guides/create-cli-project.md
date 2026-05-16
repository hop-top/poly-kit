# Create a new CLI project

Scaffold a runnable kit-based CLI in one command.

## Who this is for

Developers starting a new CLI tool and willing to use kit's
template runtime. Not for adopting kit inside an existing tool —
see Related pages.

## Before you begin

You need:

- `kit` on PATH (`kit --version`)
- A writable working directory
- Optional: `gh` (for GitHub repo creation)
- Optional: `git-hop` (for hop-style worktree init)
- Optional: `tlc` (for task tracking inside the project)

When optional binaries are missing, `kit init` skips the matching
step silently — see Troubleshooting.

## Recommended path

```bash
kit init mytool --from cli-go --hop=false -y
```

This creates `./mytool/` with a Go CLI scaffold, no hop layout, no
GitHub repo, no prompts.

## Steps

1. Pick a name (lowercase, `[a-z][a-z0-9-]{0,63}`).
2. Run `kit init mytool --from cli-go --hop=false -y`.
3. `cd mytool`.
4. Run `go mod tidy` to populate `go.sum`.
5. Run `make build` to compile.

## Verify the result

After step 5:

```bash
# No template artefacts leaked through.
find . -name '*.tmpl' | head
find . -maxdepth 1 -name 'kit-template.yaml' -o -name 'tiers.yaml'

# Project bones present.
ls .tlc/        # exists if tlc was on PATH
ls .kit/version # template lock

# Build succeeds.
make build
./bin/mytool --help
```

`find` should print nothing for `.tmpl` and the manifest files.
`make build` should exit 0.

## Troubleshooting

See [troubleshoot-scaffold.md](troubleshoot-scaffold.md) for the
full symptom-indexed list. Quick hits:

| Symptom | Fix |
|---|---|
| `git init failed: not in a git repository` | Pass `--hop=false`, or run inside a hopspace. |
| `gh: command not found` | Install gh, or pass `--account-type=none`. |
| `tlc init` did not run | Install tlc; re-run `tlc init` inside the project. |
| `go build` fails with `missing go.sum` | Known T-0032: run `go mod tidy`. |

## Optional

### Org account with public repo

```bash
kit init mytool --from cli-go \
  --account-type=org --org=acme \
  --visibility=public -y
```

`--account-type=org` requires `--org`. Module path becomes
`github.com/acme/mytool`. Branch protection on `main` is enabled
automatically.

### TS or Python scaffolds

```bash
kit init mytool --from cli-ts --hop=false -y
kit init mytool --from cli-py --hop=false -y
```

### Augment an existing project

Render extra tiers into a project that already has `.kit/version`,
using cwd as the render target:

```bash
cd existing-project
kit init --mode augment --tier 4 --from cli-go -y
```

No positional name — augment uses cwd as the target and falls back
to `basename $cwd` for the project name when needed. Passing `.`
(or any positional) makes that string the project name verbatim,
which is rarely what you want.

Differing files appear as `.kit-suggested.<name>` siblings; review
and merge by hand. No git, no GitHub, no push in augment mode.

### Augment a hop worktree or bare-worktree-shaped repo

Auto-detect refuses bare worktrees (the case where
`git rev-parse --git-common-dir` differs from `--git-dir`) and any
tree carrying a `.kit/version` marker. Hop-style worktrees fall into
the bare-worktree bucket. Bypass auto-detect with `--mode augment`:

```bash
cd ~/.w/labspace/myproj/hops/fix/widgets
kit init --mode augment --tier 2 --from cli-go --no-github -y
```

Same semantics as a regular augment: cwd is the render target, no
git/GitHub/push side-effects, existing files preserved.

### Dry run

```bash
kit init mytool --from cli-go --dry-run -y
```

Prints the planned write set; touches nothing.

## Advanced

Flag precedence (highest first): CLI flag → `KIT_<UPPER_NAME>` env
→ `defaults.yaml` → manifest variable Default → built-in default →
wizard prompt (interactive only).

Built-in defaults:

| Var | Default |
|---|---|
| `account-type` | `personal` |
| `visibility` | `private` (personal/org); `""` (none) |
| `license` | `MIT` (personal); `Apache-2.0` (org) |
| `default-branch` | `main` |
| `runtime` | `["go"]` |
| `tier` | `4` |
| `hop` | `true` |
| `theme` | `daylight` |
| `template` | `cli-go` |

## Related pages

- [troubleshoot-scaffold.md](troubleshoot-scaffold.md) — fix
  scaffold failures
- [configure-bus-enforcement.md](configure-bus-enforcement.md) —
  set topic enforcement after bootstrap
- [hook-cli-into-bus.md](hook-cli-into-bus.md) — emit your first
  event from the new CLI
