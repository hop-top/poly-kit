# Net

## What it answers

Is the process allowed to reach the network right now (`--offline`), and how is a violation refused?
`NetPolicy` records the marker; `OfflineGuard` and `OfflineGuardClient` enforce it beneath the caller.
Whether the user consented to telemetry egress is a different question, answered by
`HopTop\Kit\Telemetry`, and `HttpsSink` is deliberately not guarded here.

## Use it when

- the `--offline` flag was resolved: `NetPolicy::setOffline(true)` once at start-up
- you build a Guzzle client: `new Client(['handler' => OfflineGuard::stack()])`, or
  `OfflineGuard::push($stack)` on a stack you already own
- you inject a non-Guzzle PSR-18 client: `OfflineGuardClient::wrap($client)`
- a call site opens sockets directly (PDO, `fsockopen`, `file_get_contents`): consult
  `NetPolicy::isOffline()` yourself, the guard cannot see those

## Quick start

```php
// after require vendor/autoload.php
use GuzzleHttp\Client;
use HopTop\Kit\Net\NetPolicy;
use HopTop\Kit\Net\OfflineException;
use HopTop\Kit\Net\OfflineGuard;

NetPolicy::setOffline(true); // once, where the --offline flag is resolved

$client = new Client(['handler' => OfflineGuard::stack()]);

try {
    $client->get('https://example.com/');
} catch (OfflineException $e) {
    echo $e->getMessage(), "\n"; // GET https://example.com/: network disabled by --offline
}

var_dump(NetPolicy::isLoopbackHost('127.0.0.1:8080')); // bool(true)
```

## Contract

- A blocked request always throws `OfflineException`; nothing is skipped silently. The exception
  implements PSR-18 `ClientExceptionInterface`
- Loopback stays reachable: `localhost`, `127.0.0.0/8`, `[::1]` and an empty authority (unix socket).
  DNS names are remote even when they would resolve to loopback
- The marker is process state, not per request; long-lived servers wrap a per-client
  `OfflineGuardClient` instead of setting it
- Pushing the guard twice keeps one middleware; wrapping twice keeps one decorator
- Parity: none recorded; Go `go/core/netpolicy` is the reference

## Neighbours

- `HopTop\Kit\Api`: `ApiClient` installs this guard when no client is injected
- `HopTop\Kit\Telemetry\Sink`: `HttpsSink` is logging-class egress and stays unguarded
- `HopTop\Kit\Cli`: the future home of the `--offline` global flag

## See also

- [Offline enforcement](../../../../../docs/adopters/reference/php-sdk.md#offline-enforcement)
- [Go reference: `go/core/netpolicy`](../../../../../go/core/netpolicy/)
