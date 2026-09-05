# spaced

> Know what SpaceX is really up to — from your terminal.

[![CI](https://img.shields.io/badge/CI-passing-brightgreen)](#) [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE) [![Go](https://img.shields.io/badge/Go-1.23+-00ADD8)](go/) [![TypeScript](https://img.shields.io/badge/TypeScript-5.0+-3178C6)](ts/) [![Python](https://img.shields.io/badge/Python-3.11+-3776AB)](py/)

![demo](media/go.gif)

Satirical SpaceX historian and cross-language parity test vehicle for
`hop.top/kit/cli`: Go, TypeScript, Python and a browser build exercising
one identical command contract.

## Install and run

### Go

```sh
go install hop.top/kit/examples/spaced/go@latest
# or from the repo root: go build -buildvcs=false -o spaced ./examples/spaced/go/

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
source ../../sdk/py/.venv/bin/activate   # or prefix each call with ../../sdk/py/.venv/bin/python
python py/spaced.py mission list
python py/spaced.py elon status
python py/spaced.py fleet list
```

## Commands

| Command | Description |
| ------- | ----------- |
| `mission list` | All missions, ordered by hubris |
| `mission inspect <name>` | Single mission detail |
| `mission search <query>` | Search missions by name |
| `launch` | Initiate launch sequence (always succeeds) |
| `abort` | Abort mission (RUD not guaranteed) |
| `telemetry` | Live mission telemetry (simulated) |
| `countdown` | T-minus display |
| `fleet list` | Vehicle fleet status |
| `starship status` | Current Starship stack iteration |
| `elon status` | Latest Elon activity (DOGE advisory) |
| `ipo status` | SpaceX IPO tracker (ETA: heat death) |
| `competitor compare <name>` | Rivals, ranked charitably |
| `daemon stop --all` | Stop all daemons (results may vary) |

Config lives at `~/.config/spaced/config.yaml`.

## See also

- [The spaced example, in depth](../../docs/adopters/guides/spaced-example.md):
  completion setup, aliases, telemetry and the 9/13 compliance
  trade-off, the browser demo, releasing, the parity harness
- [`CONTRIBUTING.md`](CONTRIBUTING.md): prerequisites, workflow, command-addition checklist
- [`CITATION.cff`](CITATION.cff), [MIT license](LICENSE)
- [Sponsoring](https://github.com/sponsors/hop-top): cash, Starlink credits, or a ride on the next Crew Dragon

Not affiliated with, endorsed by, or in any way authorized by SpaceX, Elon
Musk, DOGE, NASA, the FAA, or the Starman mannequin currently past Mars.
