# hop-top/kit-engine

PHP client for the `kit serve` sidecar: local storage, version history,
sync, peers, identity.

Wire contract:
[engine-protocol.md](../../../docs/adopters/reference/engine-protocol.md).

## Install

```bash
composer require hop-top/kit-engine
```

Requires PHP 8.4+ and a `kit` binary (on `PATH`, or pass `binPath`).
Real-time events need a WebSocket client; suggested: `ratchet/pawl`.

## Development

```bash
composer install
vendor/bin/phpunit
```
