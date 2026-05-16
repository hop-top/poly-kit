# console/stage — shared `stage` CLI subcommand

Cobra subcommand factory adopters mount once into their root CLI. Every
kit-using tool gets `<tool> stage show|set|why|list` for free.

## Mount once

```go
import stagecmd "hop.top/kit/go/console/stage"

rootCmd.AddCommand(stagecmd.New(stagecmd.Config{
    ProjectResolver: func() string { return cfg.Project.ID },
    Publisher:       myDomainPublisher, // optional
    Topics:          nil,                // optional override
}))
```

## Subcommands

### show [scope]

```sh
$ tlc stage show
stage: active (default)

$ tlc stage show ops
SCOPE  STAGE       SINCE                 REASON
ops    maintenance 2026-05-01T00:00:00Z  legacy mode
```

Scope falls back to `Config.ProjectResolver` when omitted.

### set <mode> [flags]

```sh
$ tlc stage set maintenance --reason "legacy mode"
ok: ops → maintenance
```

Flags:

- `--reason <text>` — required for non-active stages.
- `--until <RFC3339|duration>` — optional auto-expiry. Past timestamps
  rejected. Duration accepts Go's `ParseDuration` syntax (`720h`).
- `--allow <cel>` / `--deny <cel>` — repeatable advisory hints.
- `--actor <id>` — identity recorded on the State.
- `--scope <name>` — explicit scope (defaults to ProjectResolver).
- `--confirm` — admin override; skips the propose veto seam.

### why [scope]

Lists the rules currently in effect for the named scope. Output mirrors
`runtime/policy/stage.yaml`.

### list

Prints every scope in `projects.yaml` with its current stage, sorted
alphabetically.

## Output formats

All commands honour `--format table|json|yaml|csv` via `kit/output`.

## See also

- `go/core/stage/README.md` — the primitive.
- `runtime/policy/stage.yaml` — default rule set.
