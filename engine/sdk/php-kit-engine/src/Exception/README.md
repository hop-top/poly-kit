# Exception

## What it answers

Which error did an engine call raise, and what does it carry? Every error the
SDK throws extends `KitEngineException`, so one `catch` covers the client.

## Use it when

- any SDK failure (spawn, startup JSON, health check, transport, bad JSON):
  catch `KitEngineException`
- the engine answered non-2xx: catch `HttpException`, read `->status`,
  `->body`, `->method`, `->path`
- `revert()` named a version that does not exist (409): catch
  `VersionConflictException`, which extends `HttpException`

## Quick start

```php
use HopTop\KitEngine\Exception\HttpException;
use HopTop\KitEngine\Exception\VersionConflictException;

try {
    $notes->revert($id, 1);
} catch (VersionConflictException) {
    // v1 is gone: nothing to revert to
} catch (HttpException $e) {
    error_log("{$e->method} {$e->path}: {$e->status} {$e->body}");
}
```

## Contract

The status-to-exception mapping follows the engine wire contract:
[engine-protocol.md](../../../../../docs/adopters/reference/engine-protocol.md).
