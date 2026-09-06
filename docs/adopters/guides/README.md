# guides

Task-oriented how-tos for adopters: one outcome per page, with the audience named up front.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`author-a-template.md`](author-a-template.md) | structure, CI and conventions for a new kit-based CLI, using `examples/spaced/` as reference | you bootstrap a CLI by hand instead of `kit init` |
| [`build-a-transport-service.md`](build-a-transport-service.md) | put your own transport in front of the command tree and inherit the serve lifecycle | you carry requests over TCP, a channel or a queue kit does not ship |
| [`choose-enforcement-mode.md`](choose-enforcement-mode.md) | pick `off`, `warn` or `strict` bus enforcement | you decide which mode to ship |
| [`cli-parity-guide.md`](cli-parity-guide.md) | the CLI contract every Go, TypeScript and Python tool must satisfy | you check a port against the shared contract |
| [`completion-user-guide.md`](completion-user-guide.md) | enable tab completion per shell for any kit-built CLI | you install completion for bash, zsh or fish |
| [`configure-bus-enforcement.md`](configure-bus-enforcement.md) | set enforcement and observe topic violations | you run a bus and want violations reported |
| [`create-cli-project.md`](create-cli-project.md) | scaffold a runnable kit-based CLI in one command | you start a new tool on kit's template runtime |
| [`encrypt-engine-data.md`](encrypt-engine-data.md) | encrypt engine SQLite documents at rest | you run the engine and need data encrypted on disk |
| [`expose-cli-over-mcp.md`](expose-cli-over-mcp.md) | mount a cobra tree as an MCP server, one tool per leaf, both protocol revisions | you want LLM hosts to call your commands |
| [`expose-cli-over-rest.md`](expose-cli-over-rest.md) | serve a cobra tree as a versioned REST API with OpenAPI | you want scripts or services to call your commands |
| [`getting-started-cli.md`](getting-started-cli.md) | first hop-top CLI in Go, TypeScript or Python | you build your first tool and want the extended walkthrough |
| [`hook-cli-into-bus.md`](hook-cli-into-bus.md) | publish events from a command and observe them with a sink | you want commands to emit events other packages react to |
| [`inspect-config-paths.md`](inspect-config-paths.md) | which config files a CLI reads, in what order, which value wins | you debug a config value that is not the one you expect |
| [`inspect-with-datasette.md`](inspect-with-datasette.md) | browse and query a `kit serve` SQLite state with Datasette | you inspect a running or stopped engine instance |
| [`kit-init.md`](kit-init.md) | `kit init` modes, flags and outputs | you bootstrap a project or add kit conventions to an existing repo |
| [`kit-template-yaml.md`](kit-template-yaml.md) | `kit-template.yaml` manifest schema | you author a template under `templates/` |
| [`migrate-to-appshell.md`](migrate-to-appshell.md) | move a hand-rolled bubbletea TUI onto `tui.AppShell` | you already have a TUI and want the shell's diff |
| [`migrate-to-served-commands.md`](migrate-to-served-commands.md) | replace a hand-written `serve` or manual `cmdsurface` mounting with built-in services | you already serve something and want the supervisor |
| [`run-the-engine.md`](run-the-engine.md) | start the engine sidecar and connect a tool to it | you have read the engine concept and want it running |
| [`secret-management-guide.md`](secret-management-guide.md) | unified secret interface and its backends | you read or write secrets and want to swap providers via config |
| [`secure-remote-serving.md`](secure-remote-serving.md) | auth beyond loopback, one permission gate, one audit trail | you serve the command tree beyond the local machine |
| [`serve-cli-over-unix-socket.md`](serve-cli-over-unix-socket.md) | newline-delimited JSON over a local Unix socket | you want local IPC with no port |
| [`serve-mcp-from-any-sdk.md`](serve-mcp-from-any-sdk.md) | one MCP mount serving both protocol revisions from Go, TypeScript, Python, Rust or PHP | you serve MCP from a non-Go SDK |
| [`serve-mcp-with-the-sdk.md`](serve-mcp-with-the-sdk.md) | MCP over the official Go SDK: prompts, resources, subscriptions | you want the SDK's full server feature set |
| [`spaced-example.md`](spaced-example.md) | completion, aliases, telemetry posture, browser demo and parity harness for `examples/spaced/` | you use spaced as the reference CLI and want the detail its README links out |
| [`telemetry.md`](telemetry.md) | anonymous-usage pipeline, off by default: consent, inspect, reset, opt out | you decide whether to enable telemetry or need to turn it off |
| [`troubleshoot-scaffold.md`](troubleshoot-scaffold.md) | symptom-indexed fixes for `kit init` failures | a `kit init` run errored or left junk behind |
| [`trust-a-peer.md`](trust-a-peer.md) | add a remote engine to the trust mesh so the two sync | you connect two engines |
| [`tui-component-gallery.md`](tui-component-gallery.md) | cross-language TUI component catalog for `go/console/tui` | you pick a component and want its API across ports |
