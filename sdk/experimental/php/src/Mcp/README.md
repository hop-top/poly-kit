# Mcp

## What it answers

How does an existing command tree get exposed to MCP clients as tools across both spec eras, with
destructive calls gated behind confirmation? `Bridge` walks a `Command` tree into leaves, `Mount`
configures the surface, `RequestHandler` serves it as PSR-15, `StdioServer` over stdin/stdout.
HTTP client policy is `HopTop\Kit\Net`; this namespace is the server side only.

## Use it when

- you expose runnable leaves to an agent: build a `Command` tree with `kit/*` annotations, wrap it in
  `Bridge` and `Mount`, hand `RequestHandler` to your PSR-15 router
- a leaf deletes or mutates shared state: annotate `kit/side-effect`; it stays blocked on MCP until
  `new Policy(allowDestructiveOn: [Surface::Mcp])`
- a leaf must be confirmed by a human: annotate `kit/requires-confirmation`; pass
  `confirmationKey` to `Mount` for the elicitation flow, or keep the `X-Confirm-Token` header gate
- a host refuses dotted tool names: `new Mount(toolNameSeparator: '_')` serves `widget_add`
- a host spawns the tool: `StdioServer::serve($bridge)` speaks newline-delimited JSON-RPC on
  stdin/stdout, replying in request order and returning when stdin closes

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Command;
use HopTop\Kit\Mcp\Mount;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\RequestHandler;
use HopTop\Kit\Mcp\Result;
use HopTop\Kit\Mcp\ServerInfo;
use Nyholm\Psr7\Factory\Psr17Factory;
use Nyholm\Psr7\ServerRequest;

$root = (new Command(name: 'app'))->addCommand(
    new Command(
        name: 'ping',
        description: 'Ping the server',
        annotations: ['kit/side-effect' => 'read'],
        runner: static fn (array $flags): Result => new Result(stdout: "pong\n"),
    ),
);

$factory = new Psr17Factory(); // any PSR-17 factory pair
$handler = new RequestHandler(
    new Bridge($root, Policy::default()),
    new Mount(serverInfo: new ServerInfo('app', '1.0.0')),
    $factory,
    $factory,
);

$request = (new ServerRequest('POST', '/mcp'))->withBody($factory->createStream(
    '{"jsonrpc":"2.0","id":1,"method":"tools/list"}',
));
echo (string) $handler->handle($request)->getBody();
// {"jsonrpc":"2.0","id":1,"result":{"tools":[{"description":"Ping the server",...
```

## Contract

- One mount answers `2024-11-05` (handshake) and `2026-07-28` (stateless envelope), chosen per
  request; a mount with no spec versions throws at construction
- Responses are bytes (sorted keys, trailing newline); PSR-7 must not re-encode them or merge headers
- Blocked destructive calls return an `isError` result at HTTP 200; `GET` and `DELETE` are 405;
  a disallowed `Origin` is 403 when `originAllowlist` is set
- `Bridge::leaves()` re-reads flags on every call, so `tools/list` on a long-lived mount stays live
- stdio has no headers: V4/V6/V7 are skipped, the verifier sees none, `X-Confirm-Token` never passes
- Parity: [`mcp-wire.json`](../../../../tests/cross-lang/fixtures/mcp-wire.json), replayed byte for byte by `tests/Mcp/WireConformanceTest.php`

## Neighbours

- `HopTop\Kit\Cli`: the Symfony commands whose leaves a `Command` tree mirrors
- `HopTop\Kit\Serve`: the long-running process that hosts this handler

## See also

- [MCP surface](../../../../../docs/adopters/reference/php-sdk.md#mcp-surface)
- [Serve MCP from any SDK](../../../../../docs/adopters/guides/serve-mcp-from-any-sdk.md)
- [Go reference: `go/transport/mcpsdk`](../../../../../go/transport/mcpsdk/README.md)
