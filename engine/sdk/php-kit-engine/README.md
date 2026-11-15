# hop-top/kit-engine

PHP client for the `kit serve` sidecar: local storage, version history,
sync, peers, identity. Wire contract:
[engine-protocol.md](../../../docs/adopters/reference/engine-protocol.md).

## Install

```bash
composer require hop-top/kit-engine
```

Requires PHP 8.4+ and a `kit` binary: pass `binPath`, set `KIT_BIN`, or
have `kit` on `PATH`. The SDK does not download one.

## Usage

```php
use HopTop\KitEngine\CollectionQuery;
use HopTop\KitEngine\KitEngine;

// Start a local engine (spawns `kit serve`, waits for /health)
$engine = KitEngine::start(['app' => 'myapp', 'encrypt' => true]);

// CRUD on a typed collection; documents are plain arrays
$notes = $engine->collection('notes');
$note  = $notes->create(['title' => 'Hello', 'body' => 'World']);
$all   = $notes->list(new CollectionQuery(limit: 10, sort: 'updated_at'));
$notes->update($note['id'], ['title' => 'Hello again']); // PUT: replaces body

// History + revert
$versions = $notes->history($note['id']);          // newest first
$notes->revert($note['id'], 1);                    // VersionConflictException if v1 missing
$topology = $notes->historyWithTopology($note['id']);
$heads    = $notes->branches($note['id'], live: true);
// Branch ops: fork($id, $seq), merge(), prune(), abandon() (see Collection)

// Identity, sync, peers
$key = $engine->identity()->publicKey();
$engine->sync()->addRemote('origin', 'https://sync.example.com', 'both');
$peers = $engine->peers()->list();

// Cleanup: POST /shutdown, then reap the process
$engine->stop();
```

Attach to a running engine:

```php
$engine = KitEngine::connect(9876, token: $token, shutdownToken: $shutdownToken);
```

### Start options

`app`, `data`, `port` (0 = ephemeral), `daemon`, `encrypt`, `noPeer`,
`noSync`, `binPath`, `client` (any PSR-18 `ClientInterface`; default
Guzzle), `startTimeout`, `healthTimeout`, `stopTimeout` (seconds).

Errors: [src/Exception](src/Exception/README.md).

### Schema validation

`new Collection(...)` accepts a `$validator` callable run on documents
passed to `create()`/`update()`; throw to veto before the request is sent.

### Not implemented

`$engine->events()` throws `LogicException`: no WebSocket client is
bundled (`ratchet/pawl` is suggested for a future events module).

## Development

```bash
composer install
vendor/bin/phpunit                          # unit tests, no kit needed
KIT_BIN=/path/to/kit vendor/bin/phpunit     # adds the real-binary test
vendor/bin/phpstan analyse
```
