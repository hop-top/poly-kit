# notebook-ts

TypeScript notebook CLI using `@hop-top/kit-engine` SDK.

## What it demonstrates

- Starting kit serve via the TS SDK (`KitEngine.start()`)
- CRUD + versioning through the typed `Collection<Note>` API
- Graceful shutdown via `engine.stop()`

## Usage

```
pnpm install
pnpm start -- new "My note" "Body text"
pnpm start -- list
pnpm start -- get <id>
pnpm start -- edit <id> "New title"
pnpm start -- delete <id>
pnpm start -- history <id>
pnpm start -- revert <id> <version>
```

Requires `kit` binary on PATH (or set via `KIT_BIN` env).
