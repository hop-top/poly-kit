# @hop-top/kit-engine

kit serve gives your TypeScript app the same capabilities as a native Go kit
app — identity, local storage, sync, peer discovery, encryption — without
reimplementing anything.

## Without vs With kit-engine

| Capability | Without | With kit-engine |
|---|---|---|
| Local storage | Roll your own DB + schema | `engine.collection("notes").create(...)` |
| Identity/signing | Custom crypto setup | `engine.identity.sign(payload)` |
| Peer discovery | Implement mDNS/Bonjour | `engine.peers.list()` |
| Sync | Build custom sync protocol | `engine.sync.addRemote(...)` |
| Encryption at rest | Integrate libsodium manually | `KitEngine.start({ encrypt: true })` |
| Version history | Manual event sourcing | `collection.history(id)` |

## Install

```bash
npm install @hop-top/kit-engine
```

The postinstall script checks PATH for `kit`; if missing, downloads the
binary for your platform from GitHub releases.

## Usage

```typescript
const { KitEngine } = require("@hop-top/kit-engine");

// Start a local engine
const engine = await KitEngine.start({ app: "myapp", encrypt: true });

// CRUD on a typed collection
const notes = engine.collection("notes");
const note = await notes.create({ title: "Hello", body: "World" });
const all = await notes.list({ limit: 10 });

// Real-time events
const events = engine.events;
events.on("notes.created", (data) => console.log("new note:", data));

// Identity
const { publicKey } = await engine.identity.publicKey();
const sig = await engine.identity.sign("my payload");

// Sync
await engine.sync.addRemote("origin", "https://sync.example.com", "both");

// Peer discovery
const peers = await engine.peers.list();

// Cleanup
await engine.stop();
```

## Connecting to existing daemon

```typescript
const engine = await KitEngine.connect(9876);
```

## API

- `KitEngine.start(opts?)` — spawn kit serve, return engine
- `KitEngine.connect(port)` — attach to running instance
- `engine.collection<T>(type)` — typed CRUD + history + revert
- `engine.events` — WebSocket event stream with auto-reconnect
- `engine.sync` — remote management
- `engine.peers` — peer discovery and trust
- `engine.identity` — key management and signing
- `engine.stop()` — graceful shutdown

## See also

- [`hop-top-kit-engine`](../py-kit-engine/README.md) — Python
  sibling SDK with the same surface
