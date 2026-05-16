# registry

Service discovery registry for kit apps using mDNS.

## Usage

```sh
# Terminal 1: announce a service
registry announce --name myapp --addr :8080 --cap "users:crud" --cap "health:get"

# Terminal 2: browse the network
registry browse

# Inspect a specific peer's capabilities
registry inspect localhost:8080

# Stream connect/disconnect events
registry watch
```

## Features

- mDNS-based peer announcement and discovery
- Capability advertisement via metadata
- HTTP /capabilities endpoint introspection
- Real-time peer connect/disconnect streaming via mesh
