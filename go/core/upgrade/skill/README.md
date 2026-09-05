# skill

## What it answers

What text goes into an AI agent skill file, or a running prompt, so the
agent keeps the tool's binary current. Performing the upgrade is
`go/core/upgrade`; this package only renders the instructions.

## Use it when

- you generate a skill file (`SKILL.md`, agent instructions) for your CLI: `Generate(PreambleOptions{...})` returns a markdown fragment
- you inject a one-line directive into a live prompt: `InlineFlow(binary, upgradeCmd, snooze)`
- you decide how pushy the agent may be: `SnoozeNever` (auto-upgrade silently), `SnoozeOnce` (accept, snooze on decline), `SnoozeAlways` (check and report only)

## Quick start

```go
fmt.Println(skill.InlineFlow("mytool", "", skill.SnoozeOnce))
// Output: [upgrade] run `mytool upgrade --auto`; on decline, snooze; continue task.
```

## Contract

- Empty `UpgradeCommand` defaults to `<binary> upgrade` in `Generate` and `<binary> upgrade --auto` in `InlineFlow`.
- `Generate` emits a `## Upgrade Preamble` section; `WhatsNewSection`, when set, is appended under `### What's New`.
- Any `SnoozeLevel` outside the three constants renders as `SnoozeAlways` in `InlineFlow` and emits no steps in `Generate`.

## Neighbours

- `go/core/upgrade`: `Checker`, `RunCLI`, the `upgrade` subcommand the text refers to.
- `go/ai/toolspec`: describes the CLI's command tree to agents; this package covers only the upgrade preamble.

## See also

- [upgrade/README.md](../README.md)
