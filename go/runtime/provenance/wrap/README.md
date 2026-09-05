# wrap

Source wrappers that stamp a `provenance.Provenance` on each call so an output field can say where its value came from without hand-built records.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`httpwrap/`](httpwrap/README.md) | `Client` over `*http.Client`; `New` tags authoritative, `NewCacheClient` tags cached | a field is filled from an HTTP response |
| [`sqlwrap/`](sqlwrap/) | `DB` over `*sql.DB`; `Wrap(db, dsn)` strips credentials from the DSN before it becomes the URL | a field is filled from a query |
| [`execwrap/`](execwrap/) | `Exec` over `os/exec`; URL `exec://<argv0>`, Version = 12-char argv hash | a field is filled from a subprocess |

## Conventions

- Every method takes `ctx` and the JSON-pointer `path` of the field it populates, and returns the `Provenance` to pair with `provenance.NewCached` or `provenance.NewSynthesized`.
- Recording goes to the `Tracker` on `ctx` (`provenance.Track`); in `ModeOff` the wrappers record nothing and return a zero `Provenance`.
- HTTP URLs pass through `provenance.Normalize` before recording so cassette cross-checks compare strings.
- `sqlwrap` and `execwrap` carry no options; their doc comments are the reference (`go doc ./sqlwrap`, `go doc ./execwrap`).
