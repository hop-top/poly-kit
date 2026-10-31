# blob-v1

## What it answers

What a local blob store must do, independent of language: put/get/delete/list/exists, key normalisation, the key-escape error, list order and staging-file hiding. Wrong place for the bus `Event` envelope (`../bus-v1/` pins `event_json.json` for Go and Rust) or S3 behaviour (Go only; not in the Rust SDK).

## Use it when

- you port or change a blob backend in any SDK: run both runners
- you add blob behaviour to Go: add a scenario first, then make every port pass it

## Quick start

```sh
make test-parity-blob
# or separately:
go test ./go/storage/blob/local/ -run ConformanceBlob
cargo test --features blob --test blob_conformance   # from sdk/rs
```

`make test-parity` includes it, so CI runs it with the rest of the parity gate.

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited. `blob.json` carries `version: "v1"` and a `description` of the step grammar.

| File | Cases | Pins |
|------|-------|------|
| `blob.json` | `scenarios[]` `{name, steps[]}` | each scenario runs on a fresh store: round trip, empty and binary payloads, overwrite, nested keys, `//` `.` `..` and leading-`/` normalisation, missing-key errors on get, idempotent delete (a missing key succeeds), prefix listing, list order (per directory ascending by name, so `a/b` precedes `a.txt`), staging files hidden from `list`, `..` escapes |

Conventions:

- Errors are matched by `kind`: `not_found` (the text embeds an OS path, so it is not pinned) and `escapes_root` (exact text `blob/local: key "<key>" escapes store root`).
- `content_type` is accepted by `put` and not stored; `list` pins key and size only.
- Runners are strict: unknown fields, a `version` other than `v1` and a corpus file no runner consumes all fail the run.
- To extend: add the scenario, run the Go runner, take the expectation from what Go produces. Never edit one to make another port pass.

## Not in the corpus

- `BlobRef` URIs: no such type exists in any SDK yet, so there is nothing to pin.
- Keys that reduce to the store root (`""`, `.`, `a/..`) and keys containing `\`: Go and Rust disagree. Rust rejects them with the escape error; Go accepts the `\` key as a plain file name and fails an empty key at rename. Decide the contract, then add vectors.

## Neighbours

- `go/storage/blob/local/conformance_test.go`: Go runner
- `sdk/rs/tests/blob_conformance.rs`: Rust runner
- `../bus-v1/`: event envelope vectors

## See also

- [`go/storage/blob/README.md`](../../go/storage/blob/README.md)
- [`sdk/rs/src/blob/README.md`](../../sdk/rs/src/blob/README.md)
