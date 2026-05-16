# Contributor documentation

You're modifying kit itself — adding packages, changing behaviour,
landing ADRs, cutting releases. These docs cover everything you need to
work on the kit codebase.

## Start here

- **[Contributing guide](contributing.md)** — dev setup, PR flow,
  conventions, testing, signing commits, branch policy.
- **[Releasing](releasing.md)** — release-please flow, version-bump
  policy, BREAKING-change protocol, per-release notes.

## Sections

- **[Architecture](architecture/)** — how kit is built: layering,
  dependency rules, package map, internal coupling. Start at
  [`architecture/architecture.md`](architecture/architecture.md) for the
  authoritative overview.
- **[Workflows](workflows/)** — recurring operational flows. Key
  entries:
  - [`workflows/release-checklist.md`](workflows/release-checklist.md)
    — pre-release checklist
  - [`workflows/extending-kit.md`](workflows/extending-kit.md) —
    registry / hook / discover / config extension model
  - [`workflows/migration-guide-kit-go-layout.md`](workflows/migration-guide-kit-go-layout.md)
    — migrating to the kit Go layout
- **[ADRs](adr/)** — every committed architecture decision, numbered
  and dated. Start at [`adr/README.md`](adr/README.md).
- **[Specs](specs/)** — design documents for scoped but not-yet-shipped
  features. Start at [`specs/README.md`](specs/README.md).
- **[Plans](plans/)** — phased implementation plans for in-flight work.
  Start at [`plans/README.md`](plans/README.md).
- **[Stories](stories/)** — user-visible feature stories. Start at
  [`stories/README.md`](stories/README.md).
- **[Audits](audits/)** — package audits accompanying ADRs. Start at
  [`audits/README.md`](audits/README.md).
- **[Contracts](contracts/)** — wire-level contracts: event topics,
  ext-discover protocol.
- **[Conformance](conformance/)** — dogfooding, threat model, CI
  integration, leak verification.

## Reference: which audience am I?

If you're building an app *on* kit rather than changing kit itself, see
[`../adopters/`](../adopters/) instead.
