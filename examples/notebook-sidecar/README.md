# notebook-sidecar

Go notebook CLI that uses `kit serve` as a sidecar process instead of
embedding kit packages directly.

## What it demonstrates

- Spawning `kit serve` as a subprocess with `--port 0` (auto-assign)
- Reading the startup JSON (port, pid, token) from stdout
- Performing CRUD over HTTP against the document engine
- Graceful shutdown via `POST /shutdown` with bearer token

## Usage

```
go build ./examples/notebook-sidecar/
./notebook-sidecar new "My first note" "Some body text"
./notebook-sidecar list
./notebook-sidecar get <id>
./notebook-sidecar edit <id> "New title" "New body"
./notebook-sidecar delete <id>
./notebook-sidecar history <id>
./notebook-sidecar revert <id> <version>
```

Set `KIT_BIN` to override the kit binary path (defaults to `kit` on PATH).
