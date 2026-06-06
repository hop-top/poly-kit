# ADR 0003: uri + hdl consolidated into cite

- **Status**: Accepted
- **Date**: 2026-06-06
- **Refs**: <https://hop.top/cite>

## Context

Two upstream libraries split responsibility for poly-URI handling in
kit consumers:

- `hop.top/uri` — scheme parsing, vanity-alias registry, action
  routing, OSC 8 hyperlink emission, OS handler-config generators
  (Go, macOS plist, Windows registry, FreeDesktop `.desktop`).
- `hop.top/hdl` — sister library scoped to OS handler-registration
  helpers. Its surface overlapped `uri/handle` enough that consumers
  imported `uri/handle` directly; `hdl` lived on as an orphan
  `replace` directive in kit's `go.mod` (no live imports) and as a
  similarly orphaned PHP dist artifact.

The split made the public story confusing — a downstream tool that
wanted poly-URI support had to reason about which sub-concern lived
in which repo, both released independently, and version drift between
the two was easy to land. Internally we stopped maintaining `hdl` as
a separate surface months ago; its replace directive in `go.mod` was
already removed upstream (kit `chore(deps): drop orphan hop.top/hdl
replace`), leaving no live consumer.

Meanwhile `hop.top/cite` shipped v0.1.0 with the consolidated story:
one module, three subpackages — `cite/scheme`, `cite/handle`,
`cite/handle/generate` — exporting the same symbols as the previous
`uri/*` packages plus `handle.Register`, `handle.SupportsHyperlinks`,
`handle.ErrUnsupported` (formerly the live core of `hdl`). The
public path `hop.top/cite` is now the canonical entry point.

## Decision

Replace `hop.top/uri v0.2.0-alpha.0` with `hop.top/cite v0.1.0` as
the dependency that backs poly-URI parsing, registry lookup, vanity
aliasing, and OS handler registration in kit's Go surface.

### Mechanical scope

- `go.mod` require: drop `hop.top/uri`, add `hop.top/cite v0.1.0`.
  No `replace` directive required — cite resolves via the public
  `proxy.golang.org` mirror.
- Imports: rewrite `hop.top/uri/scheme` → `hop.top/cite/scheme`,
  `hop.top/uri/handle` → `hop.top/cite/handle`,
  `hop.top/uri/handle/generate` → `hop.top/cite/handle/generate`
  across `go/console/{cli,output,uri}`, `go/core/id`, and
  `examples/spaced/go`.
- Doc comments mentioning the old paths in `go/core/id/doc.go` and
  `go/console/output/linkify.go` get the same substitution.
- Local consumer-side adapter package `go/console/uri/` keeps its
  directory + import path. Only the upstream library identity moves.

### Empirical surface check

Before swapping go.mod we probed cite v0.1.0 by writing a throwaway
`_probe/probe.go` that imported each cite subpackage and named every
identifier consumed in kit's tree — 19 in total:

- `scheme.{ActionRoute, DefaultPolicy, NewRegistryWithPolicy,
  ParseOption, ParseWithPolicyOptions, Policy, Registry,
  ResolvedAction, TypeRegistration, URI, VanityAlias,
  VanityCandidate, WithJSONAmbiguity, WithStrict}`
- `handle.Linkify`
- `generate.{HandlerSpec, Language, LanguageGo, Snippet}`

All 19 resolved 1:1 against cite v0.1.0 — confirmed drop-in. No
signature drift, no rename, no test coupling to defunct uri-side
internals.

## Consequences

- One canonical poly-URI library across kit. The "is this in uri or
  hdl?" question goes away.
- `go.mod` shrinks by one direct dependency. The dangling `hdl`
  replace (removed in a prior chore commit) stays gone.
- Downstream tools picking up the new kit cut inherit `hop.top/cite`
  transitively when they import kit's console packages. Adopters
  that previously imported `hop.top/uri` directly need the same
  mechanical rewrite.
- The PHP SDK lock file (`sdk/experimental/php/composer.lock`) still
  references the old `hop-top/uri` Packagist artifact via a homepage
  URL. That surface migrates on its own cadence and is out of scope
  for the Go-side swap.

## Acknowledged quirks

- cite v0.1.0 is the first public tag of the consolidated library;
  treat it as alpha-quality even though the version isn't suffixed
  `-alpha`. If a behavior gap surfaces against the legacy `uri`
  surface, file upstream rather than fork.
- The local kit package directory `go/console/uri/` keeps the name
  `uri` despite the upstream rename. Renaming the local adapter is a
  separate decision (touches every kit consumer's import path) and
  out of scope here.
