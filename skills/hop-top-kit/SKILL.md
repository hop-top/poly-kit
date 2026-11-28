---
name: hop-top-kit
description: >-
  Pick which kit port to build with: Go, TypeScript, Python, Rust or PHP, or
  the kit binary as a sidecar for any other language. Use when choosing a
  language or SDK for a tool built on kit, when adding kit to an existing
  project, when asked which port supports a capability, or when a port lacks a
  capability the task needs. Not for writing code with one port: load that
  port's skill for that.
license: MIT
metadata:
  version: "0.5.0-beta.16" # x-release-please-version
---

# hop-top-kit

kit is hop-top's toolkit for CLIs and services: config, output, serving,
telemetry, guardrails. Go is the reference implementation; other languages
get SDK ports of part of it, and a `kit` binary serves the rest over HTTP.
This skill picks the port. The port's own skill teaches the code.

## Pick a port

Apply in order; stop at the first rule that fits.

1. Existing project: run `scripts/detect-port.sh [dir]`. Stay in the
   project's language when [the matrix](references/capability-matrix.md)
   shows every capability the task needs as `done` or `partial` for that
   port, or as `unverified` with the caveat under Verify.
2. The task needs the `kit` binary, `kit init` scaffolding, or anything in
   [go-only](references/go-only.md): Go, skill `hop-top-kit-go`. A project in
   another language that needs only a Go-only capability keeps its port and
   runs the binary as a sidecar (rule 4) for that part.
3. Go is not an option: the SDKs whose Tier in the table below is
   `supported` first, then Rust (prerelease versions; compare the status
   counts), then PHP (only for capabilities its matrix column shows as
   `done`).
4. A language with no port: run the `kit` binary as a sidecar and call it
   over HTTP. Read the
   [cmd/kit README](https://github.com/hop-top/poly-kit/blob/next/cmd/kit/README.md)
   and [run the engine](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/run-the-engine.md).

`detect-port.sh` prints one line per manifest: `port=` (go, ts, py, rs,
php), `manifest=`, `kit=` (the version, `unpinned`, or `absent`), `skill=`,
`url=`. No lines: no port detected; ask the user which language. Several
lines: a polyglot project; pick the port of the component being built.

## Verify before committing

Do this before telling the user a port will do the job.

1. Find the task's capabilities in the matrix by name, grouped under the
   categories CLI and console; Config, storage and secrets; Serving and
   transports; Events, jobs and runtime; Guardrails and trust; AI and
   agents; Conformance and integrations.
2. Read the chosen port's cell and, for anything but `done`, the Evidence
   cell. Then:
   - `partial`: Evidence names the gap. Stay if the task does not need it;
     otherwise run the binary as a sidecar for that part, or use Go.
   - `unverified`: a module exists but nothing pins it to Go. Stay when the
     language is fixed and cross-tool behaviour does not matter; tell the
     user, and test the behaviour before relying on it. Otherwise Go or the
     sidecar.
   - `none` or `n/a`: no module. Go, the sidecar, or a different port.
3. The matrix is a snapshot at this skill's version. The
   [parity page](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/capability-parity.md)
   is current, and the
   [tiers page](https://github.com/hop-top/poly-kit/blob/next/docs/contributors/sdk-support-tiers.md)
   says what each tier promises.

## Ports

<!-- skills-sync:begin ports -->
| Port | Skill | Package | Install | Tier | Status counts |
|---|---|---|---|---|---|
| Go | [`hop-top-kit-go`](https://github.com/hop-top/kit/blob/main/skills/hop-top-kit-go/SKILL.md) | `hop.top/kit` | `go get hop.top/kit` | reference | 74 done |
| TypeScript | [`hop-top-kit-ts`](https://github.com/hop-top/kit-ts/blob/main/skills/hop-top-kit-ts/SKILL.md) | `@hop-top/kit` | `pnpm add @hop-top/kit` | supported | 11 done, 8 partial, 18 unverified, 36 none, 1 n/a |
| Python | [`hop-top-kit-py`](https://github.com/hop-top/kit-py/blob/main/skills/hop-top-kit-py/SKILL.md) | `hop-top-kit` | `pip install hop-top-kit` | supported | 9 done, 8 partial, 18 unverified, 38 none, 1 n/a |
| Rust | [`hop-top-kit-rs`](https://github.com/hop-top/kit-rs/blob/main/skills/hop-top-kit-rs/SKILL.md) | `hop-top-kit` | `cargo add hop-top-kit` | experimental | 13 done, 19 partial, 12 unverified, 29 none, 1 n/a |
| PHP | [`hop-top-kit-php`](https://github.com/hop-top/kit-php/blob/main/skills/hop-top-kit-php/SKILL.md) | `hop-top/kit` | `composer require hop-top/kit` | experimental | 4 done, 7 partial, 5 unverified, 57 none, 1 n/a |
<!-- skills-sync:end ports -->

## Hand over

Once a port is chosen, load its skill by name when the harness lists it.
Otherwise read its `SKILL.md` at the URL in the table; if the user wants it
installed, follow [install](references/install.md).

## Open when

| File | Open when |
|------|-----------|
| `references/capability-matrix.md` | a specific capability must be checked for a port, or a cell needs its Evidence |
| `references/go-only.md` | before promising a capability outside Go |
| `references/install.md` | the port skill is not installed and the user wants it |
| `scripts/detect-port.sh` | the project's language or kit dependency is unknown |
