# spaced

> Know what SpaceX is really up to — from your terminal.

[![CI](https://img.shields.io/badge/CI-passing-brightgreen)](#)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8)](go/)
[![TypeScript](https://img.shields.io/badge/TypeScript-5.0+-3178C6)](ts/)
[![Python](https://img.shields.io/badge/Python-3.11+-3776AB)](py/)

![demo](media/go.gif)

---

## Why

Every launch. Every RUD. Every daemon the media refuses to let
die. Track SpaceX's satirical history across three languages
with identical output.

Dual purpose: satirical SpaceX historian **and** cross-language
parity test vehicle for [`hop.top/kit/cli`][kit]. Four
implementations — Go, TypeScript, Python, browser — exercising
an identical command contract to verify output parity.

[kit]: https://github.com/hop-top/poly-kit

---

## Install

### Go

```sh
go install hop.top/kit/examples/spaced/go@latest

# Or build from source (run from repo root)
go build -buildvcs=false -o spaced ./examples/spaced/go/

spaced mission list
spaced launch --vehicle falcon-9 --pad lc-39a
spaced elon status
```

### TypeScript

Requires Node 20+.

```sh
npx tsx ts/spaced.ts mission list
npx tsx ts/spaced.ts elon status
npx tsx ts/spaced.ts fleet list
```

### Python

Requires Python 3.11+.

```sh
../../sdk/py/.venv/bin/python py/spaced.py mission list
../../sdk/py/.venv/bin/python py/spaced.py elon status
../../sdk/py/.venv/bin/python py/spaced.py fleet list
```

Or activate the venv first:

```sh
source ../../sdk/py/.venv/bin/activate
python py/spaced.py --help
```

---

## Commands

| Command | Description |
| ------- | ----------- |
| `mission list` | All missions, ordered by hubris |
| `mission inspect <name>` | Single mission detail |
| `mission search <query>` | Search missions by name |
| `launch` | Initiate launch sequence (always succeeds) |
| `abort` | Abort mission (RUD not guaranteed) |
| `telemetry` | Live telemetry (simulated) |
| `countdown` | T-minus display |
| `fleet list` | Vehicle fleet status |
| `starship status` | Current Starship stack iteration |
| `elon status` | Latest Elon activity (DOGE advisory) |
| `ipo status` | SpaceX IPO tracker (ETA: heat death) |
| `competitor compare <name>` | Rivals, ranked charitably |
| `daemon stop --all` | Stop all daemons (results may vary) |

---

## Shell Completion

Enable tab completion for flags and arguments.

### Quick Setup

#### Bash

```sh
spaced completion bash \
  > ~/.local/share/bash-completion/completions/spaced
```

#### Zsh

```sh
spaced completion zsh > "${fpath[1]}/_spaced"
```

#### Fish

```sh
spaced completion fish \
  > ~/.config/fish/completions/spaced.fish
```

Restart your shell, then verify:

```sh
spaced launch <TAB>                # mission names
spaced launch starman --orbit <TAB> # leo, geo, lunar, ...
```

---

## Aliases

Create short names for frequently used commands. Aliases
persist in `~/.config/spaced/aliases.yaml` (Go) or
`~/.config/spaced/config.yaml` (TS, under `aliases:` key).

### Create an alias

```sh
spaced alias add ml "mission list"
spaced alias add fs "fleet list"
```

### Use it

```sh
spaced ml              # => spaced mission list
spaced ml --format json # extra flags pass through
```

### List all aliases

```sh
spaced aliases
spaced aliases --format json
```

### Remove an alias

```sh
spaced alias remove ml
```

### Tab completion

Aliases appear alongside real commands in shell completion:

```sh
spaced <TAB>   # shows: mission, ml, fleet, fs, ...
```

---

## Configuration

```
~/.config/spaced/config.yaml
```

---

## Web Demo

Browser terminal — no Node APIs; pure function router bundled
via esbuild.

```sh
cd web && npm run build
cd web && npx serve . --listen 3131
```

Open `http://localhost:3131`. Type commands in the terminal UI.

Or use the Makefile:

```sh
make build-web
make serve-web
```

### Architecture — Pure-Function Router

`web/web.ts` delegates to `web/router.ts`, which re-exports the
same command logic used in the TypeScript CLI. The bundle
contains zero Node APIs — only browser-safe pure functions:

- No `process`, no `fs`, no `path`
- Same command handlers across CLI and browser
- esbuild targets `--platform=browser`; Node-specific imports
  fail loudly at build time

---

## Releasing

Tag-based releases — push a tag to trigger the corresponding
workflow:

```sh
git tag v0.2.0 && git push origin v0.2.0        # Go binary
git tag ts/v0.2.0 && git push origin ts/v0.2.0   # npm
git tag py/v0.2.0 && git push origin py/v0.2.0   # PyPI
```

---

## Architecture

Go is the source of truth. TypeScript and Python implement the
same command contract. Parity tests enforce identical output
across all three languages.

```sh
make test
# expands to: go test -tags parity ./cli/... -v -run TestParity
```

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for prerequisites, workflow,
and the command-addition checklist.

---

## License

[MIT](LICENSE)

---

## Sponsors

If you find spaced useful, consider
[sponsoring](https://github.com/sponsors/hop-top). Cash,
Starlink credits, or a ride on the next Crew Dragon all
acceptable.

---

## Citation

See [CITATION.cff](CITATION.cff).

---

## Disclaimer

Not affiliated with, endorsed by, or in any way authorized by
SpaceX, Elon Musk, DOGE, NASA, the FAA, or the Starman
mannequin currently past Mars.
