# relay

Cross-machine event relay using kit's bus with WebSocket transport,
authenticated with self-signed tokens.

## Usage

```sh
# Terminal 2: print this relay's public key for the listener
relay pubkey > peer.pub

# Terminal 1: start a listener that trusts that key
relay listen --addr :9090 --trust peer.pub

# Terminal 2: connect, signed, and subscribe
relay connect ws://localhost:9090/bus --auth --topics "app.*"
relay subscribe "app.#"

# Terminal 3: publish events
relay publish app.user.signup '{"email":"test@example.com"}'
```

## Authentication

Each relay has an Ed25519 identity keypair (`cli.WithIdentity`). With
`--auth`, `connect` signs a one-minute token with it on every dial and
redial (EdDSA, its `kid` in the header, `sub` its key id, `iss`
`relay`); a `listen --trust <pem>...` accepts only tokens those keys
signed. A listener without `--trust` accepts anyone, as before.

The token is kit's self-signed client token: the same identity mounts
`relay token create|decode|claims`, and a kit service under
`auth.mode: jwt` with `peer.pub` in `auth.jwt.public_key_files`
accepts what this relay signs.

## Features

- WebSocket-based bus bridging with automatic reconnect
- Topic filtering on connect (glob patterns)
- Self-signed token auth on the bus handshake
