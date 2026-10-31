# Route bridge payloads to CLI handlers

> Audience: two readers. CLI authors who want OS shells (share
> extensions, Shortcuts, browser extensions) to hand them URLs, text,
> files or blobs by shipping a manifest. Receiver authors who read those
> manifests and pick a handler per payload. Package README:
> [`go/bridge`](../../../go/bridge/README.md).

Package `hop.top/kit/go/bridge` is the protocol library only: payload
types, the manifest loader and the matcher. It does not run a process,
open a socket or speak HTTP. The host (receiver) loads manifests, asks
`Match` who handles each payload, and executes the winner itself. Wire
schemas: `contracts/bridge.proto` and `contracts/bridge.schema.json`.
The Go package is the only SDK implementation today; see
[capability parity](../reference/capability-parity.md).

## Payloads

A `Payload` carries an `id`, optional `source`, `source_version`,
`timestamp` (Unix ms) and `meta`, plus exactly one content kind:

| Kind | Go type | Fields |
|------|---------|--------|
| text | `*Text` | `Body`, `Mime` |
| url | `*URL` | `Href`, `Title`, `Selection` |
| file | `*File` | `Path`, `Mime`, `Size` |
| blob | `*Blob` | `Data` (inline, under 1 MiB per the type comment), `Mime`, `Filename` |

Decode from JSON with `json.Unmarshal`. `Payload.UnmarshalJSON` rejects
zero or several kinds with an error matching `bridge.ErrInvalidOneof`.
Encoding does not validate: a Payload built in Go with two kinds set
marshals fine, and `Match` then reports `ReasonInvalidPayload`.

```go
var p bridge.Payload
if err := json.Unmarshal(raw, &p); err != nil {
    if errors.Is(err, bridge.ErrInvalidOneof) { /* reject the message */ }
    return err
}
```

## Write a manifest (CLI authors)

One YAML file per CLI, installed under
`$XDG_CONFIG_HOME/hop-top/bridge.d/<cli>.yaml`. The package does not
resolve that directory for you; the receiver passes it to `Load`.

```yaml
name: mytool
version: 1.2.0
binary: mytool
mode: subprocess          # subprocess | http | socket | inproc
accepts:
  - kind: url
    priority: 10
    schemes: [http, https]
  - kind: text
    priority: 5
    mime: ["text/*"]
    max_size: 1048576     # bytes, inclusive; 0 or omitted = no limit
  - kind: file
    priority: 5
    mime: ["text/*", "application/pdf"]
invoke:
  args: ["--from-bridge"]
  env:
    MYTOOL_SOURCE: "${payload.source}"
  timeout: 30s
fallback_inproc:
  enabled: true
  socket: "$HOME/.run/mytool/bridge.sock"
```

Validation rules (`Manifest.Validate`):

- `name`, `version`, `binary` required; `mode` must be one of the four.
- `accepts` needs at least one rule.
- Rule `kind` is `url`, `text`, `file` or `blob`; `priority >= 0`;
  `max_size >= 0`.
- `url` rules require `schemes`; every other kind requires `mime`.
- `invoke.timeout` is a Go duration string (`30s`, `2m`).

Priority is per rule, so a CLI can be the default for URLs and a
fallback for files. Higher number wins. `${payload.<field>}` env
placeholders and `$VAR`/`~` in `fallback_inproc.socket` are expanded by
the dispatcher at dispatch time, not by this package.

## Load manifests (receivers)

```go
manifests, err := bridge.Load(dir)
```

- Reads every `*.yaml` file in `dir` (non-recursive), validates each,
  sorts by `Name`.
- A missing `dir` returns an empty slice and nil error (fresh install).
- An invalid or unparsable manifest is logged at warn level to stderr
  and skipped; it is not an error. One broken CLI cannot block the rest.
- Any other failure reading `dir` (permissions, not a directory)
  returns an error.

Use `m.Validate()` directly to check a manifest at build or install
time and get the first violation back.

## Match a payload

```go
res := bridge.Match(&p, manifests)
if !res.Matched {
    return fmt.Errorf("unhandled: %s: %s", res.Reason, res.Reason.Explain())
}
h := res.Manifest // Binary, Mode, Invoke, FallbackInproc
_ = res.Rule      // winning accept rule; res.Index is its position
```

Selection, in order:

1. Kind: only rules whose `kind` equals the payload's content kind.
2. Filters: scheme (url), MIME and `max_size` (others). A rule that
   rejects the payload is dropped and the next candidate is tried
   (fall-through).
3. Priority: highest wins. Ties break by manifest `Name` ascending, then
   rule index. The result does not depend on input order.

Filter details:

- URL scheme match is case-insensitive; `max_size` does not apply to
  URLs.
- MIME patterns: exact (`application/pdf`), type wildcard (`text/*`),
  or `*` / `*/*`. Case-insensitive; parameters like `; charset=utf-8`
  are ignored. A payload with no MIME hint matches no MIME rule.
- File size uses the declared `File.Size`; `0` means unstated and always
  passes. `Match` never stats the path.
- `max_size` is inclusive.

`Match` returns a `Result`, never an error. `Result.Manifest` aliases the
slice element you passed; do not mutate it.

## Report why nothing matched

`Result.Reason` is a closed `NoMatchReason` set; `ReasonMatched` is the
empty string. Only one reason is reported, chosen by precedence:

| Reason | Meaning |
|--------|---------|
| `no-manifests` | nothing installed |
| `invalid-payload` | zero or several kinds set (bug in the caller) |
| `no-kind-rule` | no installed rule accepts this kind at all |
| `too-large` | matched on type but exceeds every `max_size` |
| `mime-mismatch` | no rule's MIME patterns cover it (or no MIME given) |
| `scheme-mismatch` | no rule lists the URL's scheme |

`too-large` outranks the others because it names a handler that wanted
the payload. Helpers: `Reason.String()`, `IsValid()`, `Explain()` (one
line, empty for matched) and `bridge.AllNoMatchReasons()` for a legend
or an exhaustive switch.

## Test your manifest

Run `Load` on a temp dir holding your file, then `Match` on sample
payloads. The package's own fixtures live in
[`go/bridge/testdata/manifests`](../../../go/bridge/testdata/manifests/).

## See also

- [`contracts/bridge.schema.json`](../../../contracts/bridge.schema.json):
  JSON wire format of record.
- [Capability parity](../reference/capability-parity.md): no non-Go SDK
  module for bridge payloads yet.
