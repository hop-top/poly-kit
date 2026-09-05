# ghsecrets

## What it answers

GitHub Actions repository secrets through the `gh` CLI. Write-only by
GitHub's design: `Set`, `Delete`, `List`, `Exists` and `Metadata` go
through `gh secret ...`; `Get` reads the environment variable of the same
name, which is what a workflow run exports. Wrong package for anything
that must read a value outside Actions (`go/storage/secret/env`).

## Use it when

- `secret.Config{Backend: "ghsecrets", Repo: "owner/repo"}` after a blank import of this package; empty `Repo` lets `gh` detect the repository from the working directory
- a release or bootstrap command pushes tokens into a repo

## Contract

- Registered as `"ghsecrets"`. Open makes no call; `gh` must be installed and authenticated when a method runs.
- `Get` returns `ErrNotFound` when the env var is unset; no network read exists.
- `Metadata` reports `UpdatedAt` and visibility scopes from `gh secret list --json`.
- Tests: env fallback and interface compliance only; no `gh` invocation, no cassette.

## Neighbours

- `hop.top/kit/go/storage/secret/env`: the plain env reader.
- `hop.top/kit/go/integrations`: other `gh`-backed surfaces.

## See also

- [Secret management guide](../../../../docs/adopters/guides/secret-management-guide.md)
