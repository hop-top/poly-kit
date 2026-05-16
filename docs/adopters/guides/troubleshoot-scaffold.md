# Troubleshoot kit init

Symptom-indexed fixes for `kit init` failures.

## Who this is for

Developers running `kit init` whose run errored, produced wrong
output, or left junk behind.

## Before you begin

Confirm:

- `kit --version` works
- You ran `kit init` from a writable, empty (for bootstrap) or
  existing project (for augment) directory
- Your project name matches `^[a-z][a-z0-9-]{0,63}$`

If you haven't run `kit init` yet, start at
[create-cli-project.md](create-cli-project.md).

## Symptoms

### git init failed: not in a git repository

Cause: `--hop=true` (the default) tried to dispatch to
`git hop init`, but the current directory is not inside a
hopspace, or git-hop expected one.

Fix:

1. Re-run with `--hop=false`:
   ```bash
   kit init mytool --from cli-go --hop=false -y
   ```
2. Or `cd` into a hopspace first, then re-run with the default.

### gh: command not found

Cause: `kit init` reached the GitHub-create step and `gh` is not
on PATH.

Fix one of:

1. Install gh: <https://cli.github.com>.
2. Skip GitHub entirely with `--account-type=none`:
   ```bash
   kit init mytool --from cli-go --account-type=none -y
   ```
3. Skip just the repo create with `--no-github`:
   ```bash
   kit init mytool --from cli-go --no-github -y
   ```

### Files end in .tmpl

Cause: outdated kit binary still emits `*.tmpl` template
artefacts into the rendered project.

Fix:

1. Upgrade kit to commit `6de339f` or later, or rebuild your
   binary from main.
2. Delete the leaked files:
   ```bash
   find mytool -name '*.tmpl' -delete
   ```
3. Re-run `kit init` after upgrade to confirm clean output.

### kit-template.yaml and tiers.yaml leaked into the project

Cause: same outdated binary as the `.tmpl` symptom — the engine
copied the manifest and tier files into the render target.

Fix:

1. Upgrade kit to commit `6de339f` or later.
2. Remove the files:
   ```bash
   rm -f mytool/kit-template.yaml mytool/tiers.yaml
   ```

### go build fails with `missing go.sum`

Cause: known issue **T-0032**. The scaffold writes `go.mod` but
not `go.sum`; first build needs a tidy.

Fix:

```bash
cd mytool
go mod tidy
go build ./...
```

### Repo created public when I expected private

Cause: the default visibility flipped to `private` after commit
`6de339f`. Older binaries default to `public` for personal
accounts.

Fix:

1. Upgrade kit to commit `6de339f` or later.
2. Always pass `--visibility=private` (or `public`) explicitly to
   pin the choice, e.g.:
   ```bash
   kit init mytool --from cli-go --visibility=private -y
   ```

### tlc init didn't run

Cause: `tlc` is not on PATH. `kit init` skips the step silently
when the binary is missing — no error, no warning.

Fix:

1. Install tlc.
2. Run `tlc init` manually inside the project:
   ```bash
   cd mytool
   tlc init
   ```

### Augment mode rendered into the wrong directory

Cause: known issue **T-0002**. Augment mode uses the current
working directory as the render target; without a positional name
and an explicit `--mode bootstrap`, an augment intended as a
bootstrap can write into the parent project.

Fix:

1. For a new project, always pass the positional name **and**
   `--mode bootstrap`:
   ```bash
   kit init mytool --mode bootstrap --from cli-go -y
   ```
2. For augmenting an existing project, `cd` into it first and
   omit the positional name (cwd is the render target):
   ```bash
   cd existing-project
   kit init --mode augment --tier 4 --from cli-go -y
   ```
3. Roll back unwanted changes via your VCS before re-running.

### --org is required when --account-type=org

Cause: `--account-type=org` was set without `--org=<name>`.

Fix:

```bash
kit init mytool --from cli-go --account-type=org --org=acme -y
```

### Target directory already exists

Cause: bootstrap refuses to write into an existing directory; no
`--force` override exists for this guard.

Fix:

1. Pick a different name, or
2. Remove the existing directory after confirming you have no
   uncommitted work:
   ```bash
   ls mytool        # inspect first
   rm -rf mytool
   ```

## Related pages

- [create-cli-project.md](create-cli-project.md) — the happy-path
  walkthrough this page recovers from
