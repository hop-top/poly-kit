# check-kit-pin

## What it answers

Does the `hop.top/kit` version pinned in the cli-go template still resolve
to a recent release? A project `kit init --from cli-go` generates requires
that pin, so a stale one scaffolds projects on an old kit. Keeping the pin
in step with the release manifest is `go test ./cmd/kit/init/`, not this tool.

## Use it when

- CI or a release check must fail on a lagging pin → `make check-template-kit-pin`
- a different template, proxy or tolerance → `go run ./internal/tools/check-kit-pin -pin-file <go.mod> -proxy <url> -max-lag <n>`

## Quick start

```sh
go run ./internal/tools/check-kit-pin
```

Prints `templates/cli-go/go.mod.tmpl pins hop.top/kit <version>: current`
and exits 0 on a healthy tree.

## Contract

- Reads published versions from the Go module proxy's `@v/list`, never
  local tags: the proxy is what a generated project's `go get` resolves.
- Passes when at most `-max-lag` (default 1) published versions are newer
  than the pin. A pin newer than every published version is a release in
  flight and passes; a pin neither published nor newest fails.
- The pin file must hold exactly one `hop.top/kit` requirement with a valid
  semantic version.
- Exit codes: 0 current, 1 lagging or unknown pin, 2 usage or I/O failure.

## Neighbours

- `templates/cli-go/go.mod.tmpl` and `kit-template.yaml`: the pins it
  checks, bumped by every `kit` release PR
- [`internal/tools/`](../README.md): the other build-time Go programs

## See also

- [Releasing](../../../RELEASING.md): how the release PR bumps the pin
