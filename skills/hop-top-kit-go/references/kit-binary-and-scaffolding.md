# The kit binary and scaffolding

Open when: starting a new tool, adding kit files to an existing repo,
refreshing kit-managed files, or serving kit to a language that has no port.
Only Go ships this; other languages scaffold through it and call it.

## Pick the path

| Need | Do |
|------|----|
| new Go CLI | `kit init mytool`, then `cd mytool && go mod tidy && make build` |
| new tool in TypeScript, Python or Rust | `kit init mytool --runtime ts` (also `py`, `rs`; comma-separate for several) |
| kit files in an existing repo | `kit init --tier 2` inside it (tiers below) |
| refresh mise, devcontainer, compose, env files | `kit init --update`; gate in CI with `kit init --check` |
| add or drop a curated service in compose | `kit init --add-service redis` / `--remove-service redis` |
| preview, no writes | add `--dry-run --json` |
| no GitHub repo, no prompts | `--no-github --no-push -y` |
| a template of your own | `--from` with a built-in name, `@org/name`, a git URL or a path; `kit template list` shows built-ins |
| a language with no port needs kit state, events or sync | run the engine as a sidecar, below |

Flags and defaults are in the
[cmd/kit README](https://github.com/hop-top/poly-kit/blob/next/cmd/kit/README.md)
and `kit init --help`; do not guess them. Default tier is 4 (CI, lint,
Makefile, README, toolspec). Existing files are never overwritten: augment
writes a sibling `.kit-suggested.<file>` to merge by hand. Scaffolding
calls `git` and `gh`; a missing tool skips its step silently
([troubleshooting](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/troubleshoot-scaffold.md)).

## Tiers

| Tier | Adds |
|------|------|
| 0 | nothing (detection only) |
| 1 | ignore file, lint config, Makefile |
| 2 | tier 1 and CI workflow |
| 3 | tier 2 and `cmd/<name>/main.go` when missing |
| 4 | tier 3 and README, toolspec, conformance files |

## Add a command to a scaffolded Go tool

The scaffold's `cmd/hello.go` is the model: copy it to `cmd/<name>.go`,
rename, keep the `init()` that calls `root.Cmd.AddCommand`, and keep the
three annotations (`SetSideEffect`, `SetIdempotency`, `SetTopLevelVerb`).
The root already carries `status`, the `service start` services and the
config hook; nothing to mount. Add a `_test.go` beside it like
`cmd/hello_test.go`, then `make build` and run `./bin/<tool> <name> --format json`.

## Run the engine as a sidecar

For a language without a port, or a port missing one Go-only capability:
spawn `kit service start --port 0 --data <dir>` from the tool (port 0 picks a
free one; it logs readiness on stderr and answers `GET /health`) and call it
over localhost HTTP. Not a library: start it, wait for readiness, stop it.
Reading: [run the engine](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/run-the-engine.md),
[engine protocol](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/engine-protocol.md),
[serve MCP from any SDK](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/serve-mcp-from-any-sdk.md).

## Pages

- [kit init](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/kit-init.md): modes, flags, release-please, pinned actions
- [create a CLI project](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/create-cli-project.md): the verified first run
- [kit-template.yaml](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/kit-template-yaml.md) and
  [author a template](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/author-a-template.md): your own template
