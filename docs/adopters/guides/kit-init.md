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
(`.kit/version` present) or in a bare git worktree root.

### Auto-detect rules

`kit init` (no `--mode`) walks cwd in this order:

1. `git rev-parse --git-common-dir` differs from `--git-dir` → bare
   worktree → refused (use `--mode augment` to override).
2. `.kit/version` present → already-initialized kit repo → refused.
3. `.git/` present → augment.
4. otherwise → bootstrap.

A positional `<name>` shifts step 3/4 toward bootstrap when
`<cwd>/<name>` does not yet exist (creating a new project under
cwd, not augmenting cwd itself).

### When to use `--mode augment` explicitly

Pass `--mode augment` to bypass auto-detect when you need to
augment a repo that auto-detect refuses:

- **Bare worktrees / hop-shaped repos.** A worktree under
  `hops/<branch>/` whose `.git` is a file pointing to the bare
  parent triggers the bare-worktree refusal. `--mode augment`
  bypasses that check.
- **Labspace roots.** Any tree whose ancestors carry `.kit/`
  markers from prior augments may surface as already-initialized;
  `--mode augment` overrides.

Worked example — augment an existing hop worktree at tier 2
(lint + CI only), no GitHub side-effects:

```bash
cd ~/.w/labspace/myproj/hops/fix/widgets
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

JSON summary output is controlled by the kit-owned global flag,
`--format json` (parity contract §3.3) — there is no init-local
`--json` flag.

Precedence: `flag > env (KIT_INIT_*) > defaults file > built-in default`.
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
| 3    | tier 2 + `cmd/<name>/main.go` (only if missing)                  |
| 4    | tier 3 + `README.md`, `*.toolspec.yaml`, full conformance set    |

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
- Spec: `~/.ops/docs/superpowers/specs/2026-04-26-kit-init-design.md`
