# src

Package source. Entry point: `KitEngine`. One class per module client
(`Collection`, `SyncClient`, `PeerClient`, `IdentityClient`); `Http` is
the shared PSR-18 transport. Errors: [Exception](Exception/README.md).

## Events

`KitEngine::events(string $topic = '*', ?float $timeout = null)` returns
an `EventStream`: lazy, blocking, reconnecting. Needs `phrity/websocket`
(`composer require phrity/websocket`); without it iteration throws
`KitEngineException` with that hint.

```php
foreach ($engine->events('kit.engine.document.*') as $event) {
    echo $event->topic, ' ', json_encode($event->payload), "\n";
}
```

- Each `Event` has `topic` and `payload` (the `message` frame, see the
  [protocol](../../../../docs/adopters/reference/engine-protocol.md)).
  `welcome`, `ack`, `error` and junk frames are skipped.
- Topic: hub patterns, `*` one segment, `**` one or more. The bare
  default `*` means every topic (subscribes `**`). The pattern is sent
  in the `subscribe` frame and re-checked client side.
- Connects on first iteration; a failed first connect throws
  `KitEngineException`. A dropped socket reconnects and resubscribes
  after 1s, indefinitely.
- Auth: `/events` is unauthenticated; the engine token is still sent as
  `Authorization: Bearer` when set.
- Stop: `break`, `$stream->close()` (safe in a signal handler; the loop
  polls every 250ms), or the `$timeout` argument (seconds). A closed
  stream does not restart.
- The engine does not yet publish document events to the socket, so a
  live stream currently stays silent after the ack.
