# kit init

Bootstrap a new kit-powered CLI project, or augment an existing repo
with kit conventions. Replaces the deprecated `kit scaffold` command.

## Modes

`kit init` auto-detects mode from cwd. Override with `--mode`.

| Mode        | Trigger                              | Effect                          |
|-------------|--------------------------------------|---------------------------------|
| bootstrap   | `kit init <name>` in empty dir       | Create new project tree         |
| augment     | `kit init` (no name) in existing repo| Add tier-N kit files in place   |

Refused with hint: `kit init` in an already-initialized kit repo
(`.kit/version` present) or in a bare repository root.

### Auto-detect rules

`kit init` (no `--mode`) walks cwd in this order:

1. bare repository **root** (no working tree) → refused (use
   `--mode augment` to override). A *linked worktree* of a bare repo
   is not refused: it is a normal usable checkout, so it falls
   through to the rules below and augments like any other repo.
2. `.kit/version` present → already-initialized kit repo → refused.
3. `.git/` present → augment. A hop-style worktree lands here, since
   its `.git` is a file pointing at the bare parent.
4. otherwise → bootstrap.

A positional `<name>` shifts step 3/4 toward bootstrap when
`<cwd>/<name>` does not yet exist (creating a new project under
cwd, not augmenting cwd itself).

### When to use `--mode augment` explicitly

Pass `--mode augment` to bypass auto-detect when you need to
augment a repo that auto-detect refuses:

- **Bare repository roots.** A bare repo root (git internals, no
  working tree) is refused — scaffolding next to `HEAD`/`objects`/
  `refs` is never right. `--mode augment` bypasses that check.
- **Hop-shaped worktrees.** A worktree under `hops/<branch>/` is no
  longer refused by auto-detect, so plain `kit init` augments it.
  Passing `--mode augment` explicitly is still worthwhile: it
  resolves to the hop-aware path, which adds the dirty-tree refusal
  and the branch line in the summary described below.
- **Labspace roots.** Any tree whose ancestors carry `.kit/`
  markers from prior augments may surface as already-initialized;
  `--mode augment` overrides.

Worked example — augment an existing hop worktree at tier 2
(lint + CI only), no GitHub side-effects:

```bash
cd ~/src/myproj/hops/fix/widgets
kit init --mode augment --tier 2 --no-github -y
```

Augment uses cwd as the render target. It does not init a git
repo, create a GitHub repo, or push — those are bootstrap-only.
Existing files are preserved; differing files appear as
`.kit-suggested.<name>` siblings for diff/merge.

## Quick start

```bash
# Personal CLI (Go, default)
kit init mytool

# Org CLI, Go + TypeScript, public repo
kit init mytool --runtime go,ts --account-type org --org my-org

# Augment an existing Go repo: add lint + CI (tier 2)
cd existing-repo && kit init --tier 2

# Preview without writing
kit init mytool --dry-run --format json
```

## Flag reference

| Flag                | Default     | Notes                                                    |
|---------------------|-------------|----------------------------------------------------------|
| `--from`            | `cli-go`    | Template: built-in name, `@org/name`, git URL, or path   |
| `--module`          | derived     | Go module path; default `github.com/<owner>/<name>`      |
| `--runtime`         | `go`        | `go`, `ts`, `py` (comma-separated; multi-runtime OK)     |
| `--tier`            | `4`         | Augment tier 0–4 (see below)                             |
| `--mode`            | auto        | `bootstrap` or `augment`; empty = auto-detect            |
| `--account-type`    | `personal`  | `personal` \| `org` \| `none`                            |
| `--org`             | `""`        | GitHub org (required when `--account-type=org`)          |
| `--visibility`      | per-account | `public` \| `private` \| `internal`                      |
| `--no-github`       | `false`     | Skip GitHub repo creation                                |
| `--no-push`         | `false`     | Skip initial push                                        |
| `--license`         | per-account | License id, e.g. `MIT`, `Apache-2.0`                     |
| `--hop`             | `true`      | Use `git hop` for repo init                              |
| `--default-branch`  | `main`      | Default branch                                           |
| `--author`          | git config  | Author name; falls back to `git config user.name`        |
| `--email`           | git config  | Author email; falls back to `git config user.email`     |
| `--theme`           | `daylight`  | Theme                                                    |
| `--description`     | `""`        | Project description                                      |
| `--dry-run`         | `false`     | Preview without writing                                  |
| `--force`           | `false`     | Bypass non-destructive guards (no overwrite either way) |
| `-y`, `--yes`       | `false`     | Non-interactive: skip wizard prompts                     |
| `--with-release-please` | `true`  | Render the release-please caller (see [Release-please](#release-please)); `--without-release-please` skips it |
| `--migrate-release-please` | `false` | Replace a plain, committed hand-written release-please workflow with the caller |

JSON summary output is controlled by the kit-owned global flag,
`--format json` (see
[`cli-parity-guide.md` §"Global Flags"](cli-parity-guide.md#global-flags)):
there is no init-local `--json` flag.

Precedence: `flag > env (KIT_<UPPER_NAME>, e.g. KIT_ORG, KIT_ACCOUNT_TYPE) > defaults file > built-in default`.
Defaults live in `~/.config/kit/defaults.yaml`.

## Augment tiers

`kit init --tier <N>` adds a cumulative slice of kit conventions to
an existing repo. Existing files are never overwritten — when a file
would clash, augment writes `.kit-suggested.<filename>` next to it
for the user to diff/merge.

| Tier | Adds                                                            |
|------|-----------------------------------------------------------------|
| 0    | Nothing (pure detection / preview)                              |
| 1    | `.gitignore`, `.golangci.yml`, `Makefile` (or runtime-equivalent) |
| 2    | tier 1 + `.github/workflows/ci.yml`                              |
| 3    | tier 2 + `main.go`, `cmd/root.go`, `cmd/hello.go` (only if missing): a kit root with `serve`, the `api`, `socket`, `mcp` and `rpc` services, and a sample command |
| 4    | tier 3 + `README.md`, `*.toolspec.yaml`, full conformance set    |

## Release-please

`kit init` renders `.github/workflows/release-please.yml`: a caller
of the hop-top/.github `release-please-on-push` reusable workflow
(release-bot App token, one run per branch at a time, config check).
When the repo has neither `.github/release-please-config.json` nor
`.github/.release-please-manifest.json`, kit writes a starter pair in
the org shape: tags `<component>/v<version>` — except Go packages,
which tag bare `v<version>` so `go get` resolves them (their release PR
title reads `chore(release): <version>`) — release PR titles
`chore(release): <component> <version>`, labels
`status:release-pending` / `status:release-tagged`, and every package
on the alpha channel starting at `0.1.0-alpha.0`. One runtime gives one
package at the repo root named after the project; `--runtime go,ts,py`
puts Go at the root and each other runtime under `ts/`, `py/`, … as
`<name>-ts`, `<name>-py`. The manifest stays `{}` (first release
`0.1.0-alpha.0`) unless a python or rust package exists — those
release types ignore `initial-version`, so every package is seeded
and the first release is `0.1.0-alpha.1`.

The pair is yours from then on: kit never rewrites it, and reports
instead of writing when only one of the two exists.

The two labels must exist before the first release-please run. When
`kit init` creates the GitHub repo itself it creates them too
(idempotent). Otherwise — augment, `--no-github`, `--dry-run` — the
summary prints the commands:

```bash
gh label create status:release-pending --color ededed --force
gh label create status:release-tagged --color ededed --force
```

### Adopt the caller in a repo that already runs release-please

```bash
kit init --mode augment --tier 0 --no-github -y                  # 1. look
kit init --mode augment --tier 0 --no-github -y --migrate-release-please  # 2. replace
```

1. Without the flag nothing that runs release-please is touched: the
   caller lands as `release-please.yml.kit-suggested` and the summary
   names the existing workflow. There is never a second live
   release-please workflow.
2. With `--migrate-release-please` kit replaces the existing workflow
   when it is **plain** (one job: optional checkout, the release-bot
   App token, the action with `config-file` / `manifest-file` /
   `target-branch` / `token`; push-on-branches and/or
   `workflow_dispatch` triggers) **and** committed unchanged in git.
   Triggers, config paths and target branch carry over into the
   caller; a file named other than `release-please.yml` is removed.
   A PAT or `GITHUB_TOKEN` job switches to the release-bot App — the
   summary says so.

Anything else — extra jobs (e.g. a publish job chained on release
outputs), job outputs, run steps, other action inputs, other
triggers — is **custom**: kit reports what makes it custom and never
rewrites it. Move the extra jobs into the suggested caller (chain
them with `needs: release-please` on its outputs), then delete the
old workflow.

Re-running is a no-op once the caller is in place: kit reads its
triggers and paths back from the live file instead of re-deriving
them. Hand-edits to the caller follow the usual rule — the next run
offers a `.kit-suggested` sibling instead of overwriting.

### Publishing: one publish.yml

Every package except Go publishes through one
`.github/workflows/publish.yml`: a caller of hop-top/.github's
`publish-on-tag.yml` whose `ecosystems` map lists each release-please
component (`<name>-ts`, `<name>-py`, …) with its directory, registry
package and mirror repo. Package names and mirrors follow the org
conventions for the owner kit finds (`--org`, the origin remote, or the
module path); check them before the first release tag. A repo with a
single ts, py or rs package publishes without a mirror.

Go in a new repo gets no publish step: its release-please tags are
bare `v<version>` and proxy.golang.org serves the module straight from
them. A pure-Go repo therefore has no `publish.yml`, just
`test-go-caller.yml`. A polyglot repo whose Go package already tags
`<component>/v<version>` and ships through a mirror repo (a vanity
import such as `hop.top/<name>`) keeps its mirror-only Go entry.

If another workflow already publishes through hop-top/.github,
`publish.yml` only lands as a `.kit-suggested` sibling, so nothing
publishes twice.

Re-running `kit init` in a repo that still has the per-language
`release-<lang>-caller.yml` files an earlier version generated removes
each one that is unedited, and reports edited ones for you to delete.

## Migration from `kit scaffold`

`kit scaffold` was removed in this release. Map old commands:

| Old (`kit scaffold`)                          | New (`kit init`)                                                |
|-----------------------------------------------|-----------------------------------------------------------------|
| `kit scaffold myapp`                          | `kit init myapp`                                                |
| `kit scaffold myapp --lang go`                | `kit init myapp --runtime go`                                   |
| `kit scaffold myapp --lang go,ts`             | `kit init myapp --runtime go,ts`                                |
| `kit scaffold mytool --lang py --org my-org`  | `kit init mytool --runtime py --account-type org --org my-org`  |
| `kit scaffold myapp --no-push`                | `kit init myapp --no-push`                                      |
| `kit scaffold myapp --template <variant>`     | `kit init myapp --from <template>`                              |
| (no equivalent — manual)                      | `kit init` (no name) → augment existing repo at `--tier <N>`    |

Flag rename summary:

- `--lang` → `--runtime`
- `--template` → `--from`
- `--org` (now requires `--account-type=org`)

New flags with no `kit scaffold` equivalent: `--tier`, `--mode`,
`--account-type`, `--visibility`, `--no-github`, `--hop`,
`--default-branch`, `--license`, `--author`, `--email`, `--theme`,
`--description`, `--dry-run`, `--force`, `--yes`. (JSON summary is
opted-in via the kit-owned global `--format json`, not an init-local
flag.)

## Examples

### Personal Go CLI, push to GitHub

```bash
kit init mytool --description "Does useful things"
```

Creates `./mytool/`, runs `git init`, scaffolds Go module, creates
GitHub repo under your personal account, pushes initial commit.

### Org-owned multi-runtime CLI, private

```bash
kit init mytool \
  --runtime go,ts,py \
  --account-type org --org acme \
  --visibility private \
  --license Apache-2.0
```

### Augment an existing repo (lint + CI only)

```bash
cd legacy-repo
kit init --tier 2 --no-github
```

Adds `.gitignore`, `.golangci.yml`, `Makefile`, and CI workflow
without touching existing files.

### Dry-run with JSON output (CI / scripting)

```bash
kit init mytool --dry-run --format json
```

Emits a structured summary of files that would be written, without
touching the disk.

### Custom template from a git URL

```bash
kit init mytool --from https://github.com/acme/kit-template-acme
```

## See also

- [Author a Template](author-a-template.md) — manual
  walkthrough of what `kit init` produces
- [Getting Started CLI](getting-started-cli.md) — the kit CLI
  contract `kit init` wires up
- Source: [`cmd/kit/init/`](../../../cmd/kit/init/)
