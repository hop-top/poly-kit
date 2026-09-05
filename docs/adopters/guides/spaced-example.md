# The spaced example, in depth

> Completion setup, aliases, telemetry posture and the compliance
> trade-off, the browser demo's pure-function router, releasing, and the
> cross-language parity harness for
> [`examples/spaced/`](../../../examples/spaced/README.md).

## Who this is for

Adopters using `spaced` as a reference for their own kit-based CLI, and
anyone reading its compliance score and wanting the reasoning behind it.
The example's own README carries install and the command table; this
page carries everything a reader does not need on arrival.

## Why the example exists

Dual purpose: satirical SpaceX historian **and** cross-language parity
test vehicle for [`hop.top/kit/cli`][kit]. Four implementations, Go,
TypeScript, Python, browser, exercising an identical command contract to
verify output parity.

[kit]: https://github.com/hop-top/poly-kit

## Shell completion

Enable tab completion for flags and arguments.

### Bash

```sh
spaced completion bash \
  > ~/.local/share/bash-completion/completions/spaced
```

### Zsh

```sh
spaced completion zsh > "${fpath[1]}/_spaced"
```

### Fish

```sh
spaced completion fish \
  > ~/.config/fish/completions/spaced.fish
```

Restart your shell, then verify:

```sh
spaced launch <TAB>                # mission names
spaced launch starman --orbit <TAB> # leo, geo, lunar, ...
```

## Aliases

Create short names for frequently used commands. Aliases persist in
`~/.config/spaced/aliases.yaml` (Go) or `~/.config/spaced/config.yaml`
(TS, under the `aliases:` key).

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

## Configuration

```
~/.config/spaced/config.yaml
```

## Telemetry

spaced uses [kit-telemetry][kit-telemetry] to ship anonymous usage data
(command path, exit code, duration) when the user opts in. By default
telemetry is **OFF**: `Record` is a zero-cost no-op until both a granted
consent decision and a non-`off` mode are in place.

Three flips, any one wins:

- `--telemetry=off|anon|full`, per-invocation override (precedence #1).
- `SPACED_TELEMETRY_MODE`, env var, beats `KIT_TELEMETRY_MODE`.
- `kit telemetry enable` / consent prompt, once kit-consent lands.

When emitted, events publish on the bus topic
`spaced.telemetry.event.recorded` (the kit default
`kit.telemetry.event.recorded` is overridden via `WithTopicPrefix`).
`stdout` and `stderr` are never captured at any tier.

[kit-telemetry]: ../../../go/runtime/telemetry/README.md

## Compliance posture

spaced currently scores 9/13 on the kit-compliance e2e. The gap is a
naming collision: spaced already exposes a `telemetry` cobra command for
**mission telemetry** (simulated live launch data, listed in the
example's command table), which collides with kit-consent's expected
`telemetry status|enable|disable|reset|inspect` subcommands.

The two `telemetry` concepts are distinct on purpose:

- **Mission telemetry** (spaced-specific): satirical launch readouts,
  altitude / velocity / stage timing.
- **Runtime telemetry** (kit concept): opt-in usage events emitted via
  `hop.top/kit/go/runtime/telemetry`. See
  [telemetry compliance][kit-compliance] for the full subcommand
  contract.

For spaced to reach 13/13, one of two paths:

- **(a) Rename spaced's `telemetry` to `mission-telemetry`** (or just
  `mission`). Recommended. `telemetry` is a load-bearing kit concept;
  ceding the verb at the top level lets kit-consent's subcommands land
  where compliance expects them. Cost: a user-facing CLI break, so it
  deserves a deprecation cycle (alias the old name, log a one-time
  stderr nag, drop in the next minor).
- **(b) Register kit-consent's subcommands under spaced's existing
  `telemetry` parent.** Possible but cramped: mission-telemetry subverbs
  would become siblings of kit's `status` / `enable` / `disable` /
  `reset` / `inspect`, and the help output mixes two unrelated concept
  families. Avoid unless (a) is blocked.

The rename touches user-facing CLI, so it is deferred. The score gap is
intentional, not an oversight.

[kit-compliance]: ../reference/telemetry-compliance.md

## Web demo

Browser terminal, no Node APIs; pure function router bundled via
esbuild.

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

### Architecture: pure-function router

`web/web.ts` delegates to `web/router.ts`, which re-exports the same
command logic used in the TypeScript CLI. The bundle contains zero Node
APIs, only browser-safe pure functions:

- No `process`, no `fs`, no `path`
- Same command handlers across CLI and browser
- esbuild targets `--platform=browser`; Node-specific imports fail
  loudly at build time

## Releasing

Tag-based releases: push a tag to trigger the corresponding workflow.

```sh
git tag v0.2.0 && git push origin v0.2.0        # Go binary
git tag ts/v0.2.0 && git push origin ts/v0.2.0   # npm
git tag py/v0.2.0 && git push origin py/v0.2.0   # PyPI
```

## Parity architecture

Go is the source of truth. TypeScript and Python implement the same
command contract. Parity tests enforce identical output across all three
languages.

```sh
make test
# expands to: go test -tags parity ./cli/... -v -run TestParity
```

## See also

- [`examples/spaced/README.md`](../../../examples/spaced/README.md): install and the command table
- [`concepts/spaced-showcase.md`](../concepts/spaced-showcase.md): help, flags and output rendering on spaced
- [`author-a-template.md`](author-a-template.md): bootstrapping a CLI with spaced as the reference layout
- [`cli-parity-guide.md`](cli-parity-guide.md): the contract the parity tests enforce
- [`telemetry-compliance.md`](../reference/telemetry-compliance.md): the thirteenth factor in full
