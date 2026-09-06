# go

Go implementations of the polyglot kit library, grouped by concern.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`ai/`](ai/README.md) | AI-facing kernel packages: tool specs, extensions, LLM client | a tool talks to or is driven by an agent |
| [`bridge/`](bridge/README.md) | kit/bridge payload types and manifest loader | a non-Go shell hands data to a kit tool |
| [`conformance/`](conformance/README.md) | 12-factor adopter conformance helpers | you grade or record a CLI against the spec |
| [`console/`](console/README.md) | human-facing CLI and TUI behaviour | you build commands, output, or screens |
| [`core/`](core/README.md) | operational primitives: config, redact, scope, breaker, identity | you need a building block with no I/O of its own |
| [`integrations/`](integrations/README.md) | cross-cutting adapters; repo-host SPI | you talk to GitHub, GitLab, Gitea or Bitbucket |
| [`runtime/`](runtime/README.md) | execution logic: bus, jobs, policy, provenance, side effects | a command does work that must be observed or replayed |
| [`security/`](security/doc.go) | artifact signing, sandboxed exec, audit log, SARIF (no importable API yet; scope pinned in `doc.go` and `gaps_test.go`) | you need to trust an artifact or an execution |
| [`storage/`](storage/README.md) | persistence layers: blob, kv, secret, sql, httpcache | data must outlive the process |
| [`tools/`](tools/README.md) | static-analysis helpers for adopters | you lint a kit-based codebase |
| [`transport/`](transport/README.md) | network exposure: REST, RPC, MCP, sockets, command surfaces | a command must be reachable from outside the process |

## Conventions

- Import path root: `hop.top/kit/go/<area>/<package>`.
- Layering and what may import what: [`docs/contributors/architecture/architecture.md`](../docs/contributors/architecture/architecture.md).
