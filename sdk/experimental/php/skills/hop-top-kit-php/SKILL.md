---
name: hop-top-kit-php
description: >-
  Build a PHP CLI with the experimental kit PHP port (Composer package
  hop-top/kit, namespace HopTop\Kit). Use when PHP code requires hop-top/kit,
  when adding kit's output formats, MCP surface or serve lifecycle to a PHP
  tool, or when asked whether the PHP port can do something. Most kit
  capabilities are absent from this port: it says what exists and when to
  switch to another port or the kit sidecar.
license: MIT
metadata:
  version: "0.5.0-alpha.6" # x-release-please-version
  min_version: "0.5.0-alpha.6"
---

# hop-top-kit-php

The PHP port is experimental: published on its own prerelease line, its API may change between
prereleases. It covers a small part of kit: check the list below before promising anything, and go
to another port for the rest.

## What this port has

<!-- skills-sync:begin coverage -->
Tier: experimental.

- CLI and console. Has only: CLI factory, Output and errors, URI scheme handlers.
- Config, storage and secrets. Has only: nothing.
- Serving and transports. Has only: Serve lifecycle, Command surface bridge, REST API toolkit, MCP server, Served middleware.
- Events, jobs and runtime. Has only: Telemetry.
- Guardrails and trust. Has only: Network policy, Redaction, Delegation safety, Consent grants, Side-effect seams, TypeID, Upgrade and migrations.
- AI and agents. Has only: nothing.
- Conformance and integrations. Has only: nothing.
<!-- skills-sync:end coverage -->

Anything not listed, or listed only as a gap: open [references/other-ports.md](references/other-ports.md),
or run the `kit` binary as a sidecar; the router skill (linked there) picks between them.

## Switch when

| The task needs | Do this |
|----------------|---------|
| a capability whose cell is `none` for PHP in [coverage](references/coverage.md) | another port, or the sidecar |
| output or telemetry byte-identical to the Go tools | Go; PHP cells for these are `unverified` or `partial`, so test before claiming parity |
| `kit init`, the `kit` binary, anything under Go only in other-ports | Go, or run the binary as a sidecar |
| production use with an API that must not move | another port; this one gives no promise between prereleases |
| a framework console that is not Symfony Console | another port, or the sidecar over HTTP |

## Install and first program

Needs PHP 8.4 and Composer. `composer require hop-top/kit`; it brings Symfony Console. Everything
lives under `HopTop\Kit`, autoloaded by Composer.

The smallest program is [quickstart.php](assets/snippets/quickstart/quickstart.php): a command that
extends `KitCommand`, implements `handle()` and calls `render()`, with `Flags::register($app)`
wiring the output flags. Expected output is in
[quickstart.expected](assets/snippets/quickstart/quickstart.expected). Run it from a project that
has kit installed: `php -d auto_prepend_file=vendor/autoload.php <path to quickstart.php>`.

Package tests: `composer install && vendor/bin/phpunit`. Static analysis: `composer analyse`. Your
own tests need no Docker; only the Postgres upgrade driver needs a server and the `pdo_pgsql`
extension.

## Traps

- Subclass `KitCommand` and implement `handle()`, not `execute()`. A command with its own base class
  calls `KitOutput::for($input, $output)` instead.
- Call `Flags::register($app)` before adding commands, or `--format` and friends do not exist.
- Formats are `table`, `json` and `yaml`. Column order comes from the `ColumnSpec` list, and a
  header must equal its key.
- MCP exposure is default-closed: destructive commands stay blocked on every remote surface until a
  policy opens them.
- The offline marker guards Guzzle and PSR HTTP clients, not raw sockets, PDO or
  `file_get_contents()`; the CLI does not wire an `--offline` flag itself.
- Telemetry sends nothing without granted consent and a mode other than off.
- Upgrade drivers (file, directory, SQLite, Postgres) take your PDO handle; there is no migrate
  command tree and no release check. Read `src/Upgrade/README.md` and `src/Upgrade/Driver/README.md`
  in the installed package before writing a migration.
- `HopTop\Kit\Tui` is an empty placeholder. Spinners, status symbols and prompts are not provided by
  it.
- Only the output and delegation flags are wired: do not assume a global flag exists because Go has
  it.

## Check before you promise

1. Find the capability in [coverage](references/coverage.md). `done` is safe.
2. `partial`: the Evidence names the gap; stay only if the task avoids it.
3. `unverified`: the module exists but nothing pins it to Go; test the behaviour and tell the user.
4. `none`: not here. Use another port or the sidecar.

The coverage files are a snapshot at this skill's version; the current
[parity page](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/capability-parity.md)
is newer. Long-form PHP detail:
[PHP SDK reference](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/php-sdk.md).

<!-- skills-sync:begin version -->
## Versions

Written for 0.5.0-alpha.6. # x-release-please-version
Expects 0.5.0-alpha.6 or newer.

Installed: `composer show hop-top/kit`; `scripts/detect-port.sh` shows what the manifest pins. Older
than expected: tell the user, upgrade (`composer update hop-top/kit`). Newer than written for: this
copy may be stale; update it, then re-read it. Update by install channel, then restart the harness:
Claude Code plugin `claude plugin marketplace update hop-top`, then
`claude plugin update hop-top-kit-php@hop-top` (marketplace repository `hop-top/skillset`); gym,
`npx skills` or by hand: take `skills/hop-top-kit-php` again from `hop-top/kit-php` at `main`.
<!-- skills-sync:end version -->

## Open when

| File | Open when |
|------|-----------|
| `references/coverage.md` | a capability must be checked for PHP, with its Evidence |
| `references/other-ports.md` | the task needs something this port lacks, or you must choose another port |
| `references/cli-and-console.md` | writing commands or output formats, adding kit output to a Laravel or other framework console, the CLI root or delegation gate |
| `references/serving-and-transports.md` | hosting services under `serve`, or exposing commands over MCP |
| `scripts/detect-port.sh` | the project's language or kit dependency is unknown |
| `assets/snippets/quickstart/quickstart.php` | you need a working command to copy |
| `assets/snippets/quickstart/quickstart.expected` | you compare your output with the snippet's |
