# hop-top-kit-engine

`kit serve` gives your Python app the same capabilities as a native Go kit
app: identity, local storage, sync, peer discovery, encryption, without
reimplementing anything.

## Without vs With kit-engine

| Capability | Without | With kit-engine |
|---|---|---|
| Local storage | Roll your own DB + schema | `engine.collection("notes").create(...)` |
| Identity/signing | Custom crypto setup | `engine.identity.sign(payload)` |
| Peer discovery | Implement mDNS/Bonjour | `engine.peers.list()` |
| Sync | Build custom sync protocol | `engine.sync.add_remote(...)` |
| Encryption at rest | Integrate libsodium manually | `KitEngine.start(encrypt=True)` |
| Real-time events | Build a WS client | `engine.events("notes.*")` |

## Install

```bash
pip install hop-top-kit-engine
```

The SDK shells out to a `kit` binary on PATH: install it from the
[kit releases](https://github.com/hop-top/poly-kit/releases) or pass an
explicit path via `bin_path=`. Real-time event streaming needs the
optional `ws` extra: `pip install 'hop-top-kit-engine[ws]'`.

## Usage

```python
from kit_engine import KitEngine

# Start a local engine
engine = KitEngine.start(app="myapp", encrypt=True)

# CRUD on a typed collection
notes = engine.collection("notes")
note = notes.create({"title": "Hello", "body": "World"})
all_notes = notes.list(limit=10)

# Real-time events (requires `ws` extra)
events = engine.events("notes.*")
for event in events:
    print("event:", event)

# Identity
pubkey = engine.identity.public_key()
sig = engine.identity.sign(b"my payload")

# Sync
engine.sync.add_remote("origin", "https://sync.example.com", "both")

# Peer discovery
peers = engine.peers.list()

# Cleanup
engine.stop()
```

## API

| Call | What it does |
|------|--------------|
| `KitEngine.start(...)` | spawn `kit serve`, return engine |
| `KitEngine.connect(port)` | attach to a running instance, e.g. `KitEngine.connect(9876)` |
| `engine.collection(type)` | typed CRUD on a document type |
| `engine.events(topic)` | WebSocket event stream |
| `engine.sync` | remote management |
| `engine.peers` | peer discovery and trust |
| `engine.identity` | key management and signing |
| `engine.stop()` | graceful shutdown |

## See also

- [`@hop-top/kit-engine`](../ts-kit-engine/README.md) — TypeScript sibling SDK, same surface
- [Engine protocol](../../../docs/adopters/reference/engine-protocol.md) — the HTTP/WS routes this SDK calls
