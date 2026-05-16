# Adopter documentation

You're building an app on top of kit — a CLI, a service, or both. These
docs explain how to use kit's primitives without needing to understand
how kit itself is implemented.

## Sections

- **[Quickstart](quickstart.md)** — 10-minute tutorial: scaffold a kit
  CLI, add a command, expose it over REST.
- **[Concepts](concepts/)** — the mental models you need: CLI contract,
  bus topics, engine sidecar, storage layers, themes, output hints,
  policy engine. Read these before reaching for a guide.
- **[Guides](guides/)** — task-oriented how-tos. "How do I add shell
  completion?", "How do I wire a secret backend?", "How do I publish
  events to the bus?".
- **[Reference](reference/)** — exact details: annotations, flags, exit
  codes, config shapes, JSON schemas, API signatures.
- **[Integrations](integrations/)** — connecting kit to specific tools
  (Claude Code permissions, repo hosts, toolspec adopter/harness flows).

## Key entry points

**Concepts**

- [`concepts/bus-overview.md`](concepts/bus-overview.md) — in-process pub/sub primer
- [`concepts/engine-overview.md`](concepts/engine-overview.md) — sidecar engine: what it is, when to run it
- [`concepts/notifications-overview.md`](concepts/notifications-overview.md) — outbound notification recipes
- [`concepts/storage-abstractions.md`](concepts/storage-abstractions.md) — pick the right storage layer
- [`concepts/spaced-showcase.md`](concepts/spaced-showcase.md) — sample-app architecture walk

**Guides**

- [`guides/getting-started-cli.md`](guides/getting-started-cli.md) — extended walkthrough
- [`guides/kit-init.md`](guides/kit-init.md) — `kit init` flow + flag reference
- [`guides/create-cli-project.md`](guides/create-cli-project.md) — scaffold a new CLI
- [`guides/hook-cli-into-bus.md`](guides/hook-cli-into-bus.md) — first publish/subscribe
- [`guides/run-the-engine.md`](guides/run-the-engine.md) — start the engine sidecar
- [`guides/secret-management-guide.md`](guides/secret-management-guide.md) — secret backend recipes
- [`guides/tui-component-gallery.md`](guides/tui-component-gallery.md) — TUI components catalog

**Reference**

- [`reference/cli-api-reference.md`](reference/cli-api-reference.md) — Go CLI factory
- [`reference/ts-api-reference.md`](reference/ts-api-reference.md) — TypeScript CLI factory
- [`reference/py-api-reference.md`](reference/py-api-reference.md) — Python CLI factory
- [`reference/bus-api.md`](reference/bus-api.md) — bus types, methods, sinks
- [`reference/engine-protocol.md`](reference/engine-protocol.md) — HTTP/WS wire format
- [`reference/engine-security.md`](reference/engine-security.md) — identity, trust, encryption
- [`reference/compliance-api.md`](reference/compliance-api.md) — 12-factor checker

## Reference: which audience am I?

If you're modifying kit itself (touching `go/`, `incubator/`, `sdk/`,
`engine/`, ADRs, or contributor specs), you're a contributor — see
[`../contributors/`](../contributors/) instead.
