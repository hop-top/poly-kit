# tools/

Build-time generators. Not part of the kit binary, not published.

Each subdirectory is a standalone Go program that vendors a
third-party rule corpus into kit at a pinned upstream tag, with
provenance headers, license, and attribution. Output files are
generated, embedded via `//go:embed`, and committed to the repo.

| Tool | Purpose | Output | Refresh target |
|------|---------|--------|----------------|
| [`vendor-gitleaks/`](vendor-gitleaks/main.go) | Vendor gitleaks secret-detection rules | `go/core/scope/rules/` | `make refresh-secret-rules` |
| [`vendor-presidio/`](vendor-presidio/main.go) | Vendor Microsoft Presidio PII rules | `go/core/redact/rules/` | `make refresh-pii-rules` |

Refresh both at once: `make refresh-rules`.

## Properties

- **Idempotent.** Same `--tag` (and unchanged `curated.go`, for
  presidio) → byte-identical output. Safe to re-run.
- **Pinned.** Tags resolve to commit SHAs at fetch time so
  re-tagging upstream cannot silently change content.
- **Self-documenting.** Each generator's package comment in
  `main.go` is the authoritative usage reference; `go doc
  ./tools/<name>` prints it.

## When to add a new tool here

Use this directory when:

- The output is a vendored third-party artefact (rules,
  schemas, fixtures) embedded into kit at build time.
- It needs provenance tracking (tag, SHA, license, attribution).
- It runs from `go run ./tools/<name>` and is invoked from a
  `make refresh-*` target, not from the runtime binary.

For ad-hoc scripts or one-off generators, prefer `scripts/`.

## See also

- [`go/core/scope/rules/SOURCES.md`](../go/core/scope/rules/SOURCES.md)
  — gitleaks provenance + refresh notes
- [`go/core/redact/README.md`](../go/core/redact/README.md)
  — Presidio rule maintenance + "when to refresh" guidance
