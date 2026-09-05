# tools

static-analysis helpers adopters run over their own code.

## Sub-packages

- [provenancelint/](provenancelint/): `go/analysis` Analyzer flagging
  `provenance.Synthesized[T]` / `Cached[T]` struct fields that would
  silently break the strict-mode contract — missing json tags,
  zero-value literals, and `Provenance` declared as a sibling field.

Ships a standalone driver at
[`provenancelint/cmd/provcheck/`](provenancelint/cmd/provcheck/):

```sh
go install hop.top/kit/go/tools/provenancelint/cmd/provcheck
go vet -vettool=$(go env GOPATH)/bin/provcheck ./...
```

Wiring notes live in
[`../runtime/provenance/README.md`](../runtime/provenance/README.md).
