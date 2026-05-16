# Bootstrap or run the kit engine

`kit` is a sidecar binary that exposes the kit stack (identity,
storage, sync, peer discovery, events) over localhost HTTP/WS. Spawn
it from any language to make your tool a first-class kit node.

This page covers two intents:

1. **Bootstrap a new project** with `kit init` (most readers start here).
2. **Run the engine** for an existing app and call its HTTP API.

## Who this is for

- Tool authors creating a new CLI from scratch (`kit init`).
- App teams that want shared identity / sync without writing native
  bindings (`kit serve`).
- Polyglot teams: TS or Python apps spawn `kit` as a subprocess and
  talk HTTP.

## Before you begin

You need:

- Go 1.26 or newer (`go install`; toolchain pinned via `go.mod`)
  **or** Homebrew (`brew install`) **or** the prebuilt release
  tarball.
- `git` and `gh` on `PATH` for `kit init` repo creation.
- Default branch + author identity from `git config` (or pass
  `--author` / `--default-branch`).

```bash
go install hop.top/kit/cmd/kit@latest
# or
brew install hop-top/tap/kit
# or
curl -fsSL https://github.com/hop-top/poly-kit/releases/latest/download/kit_$(uname -s)_$(uname -m).tar.gz | tar xz
```

Verify:

```bash
kit --version
```

---

## Bootstrap a new CLI project

### Recommended path

```bash
kit init mytool
cd mytool
make build && ./bin/mytool --help
```

Default behaviour:

- Runtime: Go.
- GitHub repo: private, under your personal account.
- Tier: 4 (full conformance — CI, lint, Makefile, README, toolspec).
- Initial commit + push to `origin/main`.

### Common variants

```bash
# Multi-runtime under a GitHub org
kit init mytool --runtime go,ts --account-type org --org my-org

# Python-only, no remote repo, no push
kit init mytool --runtime py --no-github --no-push

# Augment an existing repo at tier 2 (lint + CI only)
cd existing-repo && kit init --tier 2

# Preview without touching disk
kit init mytool --dry-run --json
```

### Verify the result

```bash
ls mytool/
# .github/  cmd/  Makefile  README.md  go.mod  ...

cd mytool && make build
./bin/mytool --help
./bin/mytool --version
```

If `--no-github` was not set, the repo URL is printed at the end of
the command. Open it to confirm the initial push.

### Modes

| Mode      | Trigger                        | Effect                         |
|-----------|--------------------------------|--------------------------------|
| bootstrap | `kit init <name>` in empty dir | Create new project tree        |
| augment   | `kit init` in existing repo    | Add tier-N kit files in place  |

Mode is auto-detected; override with `--mode`.

### Augment tiers

| Tier | Adds                                                       |
|------|------------------------------------------------------------|
| 0    | Nothing (pure detection)                                   |
| 1    | `.gitignore`, `.golangci.yml`, `Makefile` (or runtime equiv) |
| 2    | tier 1 + `.github/workflows/ci.yml`                        |
| 3    | tier 2 + `cmd/<name>/main.go` (only if missing)            |
| 4    | tier 3 + `README.md`, `*.toolspec.yaml`, conformance files |

Existing files are never overwritten. Augment writes a sibling
`.kit-suggested.<filename>` for diff/merge by the user.

### `kit init` flag reference

| Flag                | Default     | Description                                              |
|---------------------|-------------|----------------------------------------------------------|
| `--from`            | `cli-go`    | Template spec (built-in name, `@org/name`, git URL, path) |
| `--module`          | derived     | Go module path (default `github.com/<owner>/<name>`)     |
| `--runtime`         | `go`        | Runtimes to scaffold (`go`, `ts`, `py`; comma-sep)       |
| `--tier`            | `4`         | Augment tier (0–4); 4 = full conformance                 |
| `--mode`            | auto        | Override detect: `bootstrap` \| `augment`                |
| `--account-type`    | `personal`  | GitHub account type: `personal` \| `org` \| `none`       |
| `--org`             | `""`        | GitHub org (required when `--account-type=org`)          |
| `--visibility`      | `private`   | `public` \| `private` \| `internal`                      |
| `--no-github`       | `false`     | Skip GitHub repo creation                                |
| `--no-push`         | `false`     | Skip initial push                                        |
| `--license`         | per-account | License id (e.g. `MIT`, `Apache-2.0`)                    |
| `--hop`             | `true`      | Use `git hop` for repo init                              |
| `--default-branch`  | `main`      | Default branch name                                      |
| `--author`          | git config  | Author name                                              |
| `--email`           | git config  | Author email                                             |
| `--theme`           | `daylight`  | Theme                                                    |
| `--description`     | `""`        | Project description                                      |
| `--dry-run`         | `false`     | Preview without writing                                  |
| `--json`            | `false`     | Emit summary as JSON                                     |
| `--force`           | `false`     | Bypass non-destructive guards                            |
| `-y`, `--yes`       | `false`     | Non-interactive: skip wizard prompts                     |

### Migrating from `kit scaffold`

`kit scaffold` was removed. Map old invocations to `kit init`:

| Old (`kit scaffold`)                          | New (`kit init`)                                     |
|-----------------------------------------------|------------------------------------------------------|
| `kit scaffold myapp`                          | `kit init myapp`                                     |
| `kit scaffold myapp --lang go,ts`             | `kit init myapp --runtime go,ts`                     |
| `kit scaffold mytool --lang py --org my-org`  | `kit init mytool --runtime py --account-type org --org my-org` |
| `kit scaffold myapp --no-push`                | `kit init myapp --no-push`                           |
| `kit scaffold myapp --template <variant>`     | `kit init myapp --from <template>`                   |

---

## Run the engine

### Recommended path

```bash
kit serve --port 8080 --data ./myapp-data
```

Output:

```
listening on http://localhost:8080
```

Then call the HTTP API from any language:

```bash
curl -X POST http://localhost:8080/notes/ -d '{"title":"Hello"}'
curl http://localhost:8080/notes/
```

### Verify the result

```bash
curl http://localhost:8080/health
# {"status":"ok"}

curl http://localhost:8080/capabilities
# {service:"kit", version:"...", resources:[...]}
```

### `kit serve` flag reference

| Flag         | Default       | Description                                |
|--------------|---------------|--------------------------------------------|
| `--port`     | `0` (random)  | HTTP listen port                           |
| `--data`     | `./data`      | SQLite + identity storage directory        |
| `--daemon`   | `false`       | Shared daemon mode (PID file, multi-app)   |
| `--app`      | `""`          | App namespace for data isolation           |
| `--no-peer`  | `false`       | Disable mDNS peer discovery                |
| `--no-sync`  | `false`       | Disable sync subsystem                     |
| `--encrypt`  | `false`       | Encrypt SQLite at rest                     |

### HTTP API reference

#### Documents

```bash
# Create
curl -X POST http://localhost:8080/notes/ -d '{"id":"n1","title":"First note"}'

# List
curl 'http://localhost:8080/notes/?limit=10&offset=0&sort=created_at&search=first'

# Get / Update / Delete
curl http://localhost:8080/notes/n1
curl -X PUT http://localhost:8080/notes/n1 -d '{"title":"Updated note"}'
curl -X DELETE http://localhost:8080/notes/n1

# History + revert
curl http://localhost:8080/notes/n1/history
curl -X POST http://localhost:8080/notes/n1/revert -d '{"seq":1}'
```

#### Sync

```bash
curl -X POST http://localhost:8080/sync/remotes \
  -d '{"name":"laptop","url":"http://192.168.1.5:8080","mode":"bidirectional"}'
curl -X DELETE http://localhost:8080/sync/remotes/laptop
curl http://localhost:8080/sync/status
curl -X POST http://localhost:8080/sync/push -d '{"diffs":[...]}'
curl http://localhost:8080/sync/pull
```

#### Identity

```bash
curl http://localhost:8080/identity
curl -X POST http://localhost:8080/identity/verify \
  -d '{"payload":"hello","signature":"...","pubkey":"..."}'
```

#### Peers

```bash
curl http://localhost:8080/peers
curl -X POST http://localhost:8080/peers/abc123/trust
curl -X POST http://localhost:8080/peers/abc123/block
```

#### Meta

```bash
curl http://localhost:8080/capabilities
curl http://localhost:8080/health
wscat -c ws://localhost:8080/events
```

---

## Optional: shared daemon mode

Multiple apps share one engine instance to reduce resource usage.

```bash
# Start daemon
kit serve --daemon --port 8080 --data ~/.kit

# App A — namespace via header
curl -X POST http://localhost:8080/notes/ \
  -H "X-Kit-App: app-a" -d '{"title":"From app A"}'

# App B — separate namespace
curl -X POST http://localhost:8080/notes/ \
  -H "X-Kit-App: app-b" -d '{"title":"From app B"}'
```

Engine writes a PID file to `--data`. Apps discover the port via PID
file or stdout JSON on spawn. Shutdown: `POST /shutdown` or SIGTERM.

## Optional: cross-language usage

### TypeScript

```ts
import { KitEngine } from '@hop-top/kit-engine'

const engine = await KitEngine.start({ app: 'myapp', port: 0 })
const notes = engine.collection<Note>('notes')
await notes.create({ id: 'n1', title: 'Hello' })
const all = await notes.list({ search: 'Hello' })
await engine.stop()
```

### Python

```python
from kit_engine import KitEngine

engine = KitEngine.start(app='myapp')
notes = engine.collection('notes')
notes.create({'id': 'n1', 'title': 'Hello'})
all_notes = notes.list(search='Hello')
engine.stop()
```

Both SDKs spawn the `kit` binary as a subprocess, read the port from
stdout JSON, then talk HTTP. No FFI, no CGo, no reimplementation.

## Advanced: peer mesh

Two engines on a LAN discover each other via mDNS:

```
┌─────────────────┐          mDNS          ┌─────────────────┐
│  Go app (kit)   │◄──────────────────────►│  TS app (kit)   │
│  port 8080      │                         │  port 8081      │
│  peer: go-node  │    sync push/pull       │  peer: ts-node  │
└─────────────────┘◄──────────────────────►└─────────────────┘
```

1. Both start with peer discovery enabled (default).
2. mDNS announces presence.
3. Each peer appears in `GET /peers`.
4. Trust established: `POST /peers/:id/trust`.
5. Sync replicates documents bidirectionally.
6. Both nodes operate offline; sync resumes on reconnect.
