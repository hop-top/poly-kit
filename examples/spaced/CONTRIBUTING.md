# Contributing to spaced

Thanks for your interest. This guide covers what you need to
get started.

---

## Prerequisites

- Go 1.26 (toolchain pinned via `go.mod`)
- Node 22
- Python 3.13
- Make

---

## Workflow

1. Fork the repo
2. Create a feature branch (`git checkout -b feat/my-thing`)
3. Make your changes
4. Run `make test` and `make lint`
5. Commit using
   [Conventional Commits](https://conventionalcommits.org)
6. Open a PR against `main`

---

## Adding a New Command

All three CLIs must implement every command. Follow this
checklist:

1. **Go** — add `go/cmd/<name>.go`; register in `go/main.go`
   via `root.Cmd.AddCommand(cmd.<Name>Cmd(root))`
2. **TypeScript** — add `ts/commands/<name>.ts`; import and
   register in `ts/spaced.ts`
3. **Python** — add `py/commands/<name>.py`; register app in
   `py/spaced.py`
4. **Web router** — export command handler from
   `web/router.ts` so the browser demo stays in sync
5. **Parity test** — add test cases to the parity suite so CI
   catches drift

---

## Parity Tests

Parity tests verify identical output across Go, TypeScript,
and Python. They live at the repo root:

```sh
make test
# expands to: go test -tags parity ./cli/... -v -run TestParity
```

Every new command needs a corresponding parity test case.

---

## Code Style

- Follow existing patterns in each language
- No new dependencies without vetting (`rsx analyze`)
- Keep files under ~500 lines; split if needed

---

## Makefile

```sh
make help        # list all targets
make build       # build all 4 implementations
make test        # run parity tests
make lint        # run linters
make clean       # remove build artifacts
```
