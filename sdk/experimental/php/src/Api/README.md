# Api

## What it answers

How does a CLI call a JSON CRUD endpoint with bearer auth, offline enforcement and a typed error
envelope, without every command re-doing Guzzle setup? `ApiClient` covers create, get, list,
update and delete against one base URL. Network policy itself lives in `HopTop\Kit\Net`; this
package only consumes it.

## Use it when

- a command reads or writes one resource collection over HTTPS: `new ApiClient($baseURL, $token)`
- the transport must be controlled (tests, custom middleware): inject `httpClient:` and guard it
  yourself with `OfflineGuard::push()` or `OfflineGuardClient::wrap()`
- a 4xx or 5xx must be handled by code: catch `ApiException` and read `$status` and `$errorCode`

## Quick start

```php
// after require vendor/autoload.php
use GuzzleHttp\Client;
use GuzzleHttp\Handler\MockHandler;
use GuzzleHttp\HandlerStack;
use GuzzleHttp\Psr7\Response;
use HopTop\Kit\Api\ApiClient;
use HopTop\Kit\Api\ApiException;

// A mocked transport stands in for the network; omit httpClient in production
// and ApiClient builds a Guzzle client with the offline guard installed.
$http = new Client(['handler' => HandlerStack::create(new MockHandler([
    new Response(200, ['Content-Type' => 'application/json'], '{"id":"42","name":"foo"}'),
    new Response(404, ['Content-Type' => 'application/json'], '{"status":404,"code":"not_found","message":"no 43"}'),
]))]);

$api = new ApiClient(baseURL: 'https://api.example', authToken: 'token', httpClient: $http);

var_export($api->get('42')); // ['id' => '42', 'name' => 'foo']
echo "\n";

try {
    $api->get('43');
} catch (ApiException $e) {
    echo $e->status, ' ', $e->errorCode, ' ', $e->getMessage(), "\n"; // 404 not_found no 43
}
```

## Contract

- Paths: `POST /`, `GET /`, `GET /{id}`, `PUT /{id}`, `DELETE /{id}` under `rtrim($baseURL, '/')`;
  ids are `urlencode`d
- Every request sends `Accept: application/json` and, with a token, `Authorization: Bearer <token>`
- Redirects are capped at 5, strict, and confined to `https`; caller options cannot widen this
- Status 400 and above throws `ApiException`; a JSON body with `status`, `code`, `message` is mapped,
  anything else yields code `http_error`
- An injected client is left untouched: its owner decides the offline guard
- Parity: none recorded; no Go counterpart package exists

## Neighbours

- `HopTop\Kit\Net`: the `--offline` marker and the guard this client installs by default
- `HopTop\Kit\Output`: `CliError` for turning an `ApiException` into an exit code

## See also

- [Offline enforcement](../../../../../docs/adopters/reference/php-sdk.md#offline-enforcement)
- [Client hardening notes](../../../../../docs/adopters/reference/php-sdk.md#client-hardening-https-sink)
