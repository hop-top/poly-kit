# router

## What it answers

"How do I start, stop, list and inspect RouteLLM server instances from
the CLI?" Provides the `kit llm router` subtree. Router configuration
types and defaults live in `hop.top/kit/go/ai/llm/routellm`; this
package only spawns and tracks processes.

## Use it when

- mount the subtree under an `llm` parent → `llmCmd.AddCommand(router.Cmd())`
- launch `python -m routellm.openai_server` from a config → `kit llm router start [config] [--daemon] [--pid <path>]`
- terminate a running instance by PID or slug → `kit llm router stop [PID|slug]`
- see which instances are alive → `kit llm router list`
- print the resolved YAML config → `kit llm router config`

## Quick start

```go
cmd := router.Cmd()
fmt.Println(cmd.Use)
for _, sub := range cmd.Commands() {
    fmt.Println(" ", sub.Name())
}
```

## Contract

- Config path: argument to `start`, else
  `$XDG_CONFIG_HOME/hop/llm/router/config.yaml`. A missing file yields
  `routellm.DefaultRouterConfig()`; a present file is YAML-unmarshalled
  over those defaults.
- PID files: `$XDG_STATE_HOME/hop/llm/router/<slug>.pid`. Slug is the
  lowercased first router name, else `default`; `--pid` or the config
  `pid_file` override the path. Slugs must match `[a-zA-Z0-9][a-zA-Z0-9_-]*`.
- `start` requires `python` on PATH, polls `<base_url>/health` for up to
  30 seconds, and prints a warning on failure rather than exiting.
  Without `--daemon` it blocks until the server exits.
- `stop` sends SIGTERM, waits up to 10 seconds for exit, then removes
  the PID file when the target was resolved from a slug. A numeric
  argument is used as a PID directly and no file is removed.
- `list` removes stale PID files as a side effect and prints
  `no running instances` when none are alive.
- Output is plain text; these subcommands do not use `--format`.

## Neighbours

- `hop.top/kit/go/ai/llm/routellm`: RouterConfig, DefaultRouterConfig,
  client-side routing.
- `hop.top/kit/go/core/xdg`: ConfigDir and StateDir resolution.
- `hop.top/kit/go/console/cli`: root command and global flags the
  subtree is mounted under.

## See also

- [docs/adopters/reference/go-primitives.md](../../../../docs/adopters/reference/go-primitives.md)
