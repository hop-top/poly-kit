# relay

Cross-machine event relay using kit's bus with WebSocket transport.

## Usage

```sh
# Terminal 1: start listener
relay listen --addr :9090

# Terminal 2: connect and subscribe
relay connect ws://localhost:9090/bus --topics "app.*"
relay subscribe "app.#"

# Terminal 3: publish events
relay publish app.user.signup '{"email":"test@example.com"}'
```

## Features

- WebSocket-based bus bridging with automatic reconnect
- Topic filtering on connect (glob patterns)
- Ed25519 identity for auth-ready transport
