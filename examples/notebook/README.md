# notebook

Personal note-taking CLI that exercises most of kit's packages.

## Packages used

- `cli` — root command factory, identity, peers, API serve
- `domain` — Entity, Repository, Service, Query
- `domain/sqlite` — SQLiteRepository with scan/bind functions
- `domain/version` — VersionedRepository + DAG for history/revert
- `sync` — Replicator, HTTPTransport, RemoteSet
- `api` — ResourceRouter, OpenAPI, middleware
- `identity` — auto-generated keypair on first run
- `peer` — mesh discovery and trust
- `sqlstore` — SQLite store with migration support

## Run

```sh
go run ./examples/notebook new "Meeting notes" --body "Discussed roadmap"
go run ./examples/notebook list
go run ./examples/notebook get <id>
go run ./examples/notebook edit <id> --title "Renamed"
go run ./examples/notebook history <id>
go run ./examples/notebook serve --addr :9090
```

## Test

```sh
go test ./examples/notebook/
```
