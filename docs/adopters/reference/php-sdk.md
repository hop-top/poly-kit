# PHP SDK Reference

> Every long-form surface of the experimental `hop-top/kit` package
> (`sdk/experimental/php`): the URI facade, output formatting rules, the
> MCP mount over PSR-15, offline enforcement and telemetry. The package
> front page is
> [`sdk/experimental/php/README.md`](../../../sdk/experimental/php/README.md),
> which carries install, the namespace table and the contract.

## Who this is for

PHP authors who already require the package and need the detail its
front page links out to.

## URI facade

The experimental SDK exposes a thin facade over `hop-top/cite` so kit callers can
use the shared URI contract without depending on kit-specific parsing code.

```php
<?php

use HopTop\Cite\ActionRoute;
use HopTop\Cite\Policy;
use HopTop\Kit\Uri\UriFacade;

$uri = UriFacade::parse('task://hop-top/uri/T-0001');
echo $uri->namespace; // hop-top/uri
echo UriFacade::canonical($uri); // task://hop-top/uri/T-0001

$policy = new Policy(
    defaultNamespaceSegments: 1,
    schemeNamespaceSegments: ['tlc' => 2],
    actionRoutes: [
        'task.claim' => new ActionRoute(
            command: 'tlc',
            args: ['-C', '{namespace}', 'task', 'claim', '{id}'],
        ),
    ],
);

$actionUri = UriFacade::parse('tlc://org/repo/T-0001?action=task.claim', $policy);
$plan = UriFacade::resolveAction($actionUri, $policy);
```

This facade intentionally delegates to `hop-top/cite` (`^0.2.0`); it does not
reimplement URI parsing, vanity handling, action routing, or handler identity.

## Output formatting

`HopTop\Kit\Output` ships the `table`, `json` and `yaml` built-in
formatters. `csv` and `text` are **not implemented in PHP** — see
*Conformance status* below.

### Column ordering

Go, the reference runtime, reads column order off `table:""` struct tags
in field declaration order. PHP rows are associative arrays, so an
explicit `ColumnSpec` list carries that order instead. Five rules bind
every runtime.

**1. Default order.** With a `ColumnSpec` list passed to
`Dispatcher::dispatch()` / `KitCommand::render()` / `KitOutput::columns()`
and no `--cols`, the list's order and names drive every formatter.
Payload key order is the fallback used *only* when no list is supplied.

```php
$rows = [
    ['count' => 3, 'status' => 'ready', 'name' => 'alpha'],
    ['count' => 8, 'status' => 'held',  'name' => 'beta'],
];
$cols = [
    new ColumnSpec('name', 'name', 9),
    new ColumnSpec('count', 'count', 7),
    new ColumnSpec('status', 'status', 5),
];
```

The payload's own key order is `count, status, name`; the spec wins:

```
name   count  status
alpha  3      ready
beta   8      held
```

**2. `--cols` reorders as well as selects.** The user's sequence beats
the `ColumnSpec` order — `--cols status,name` renders `status` then
`name` — and the same rule holds on the no-schema fallback path:

```
status  name
ready   alpha
held    beta
```

`--format json` follows the identical order, since PHP arrays are
insertion-ordered end to end:

```json
[
  {
    "status": "ready",
    "name": "alpha"
  },
  {
    "status": "held",
    "name": "beta"
  }
]
```

**3. `header === key`.** The two name the same column: the label, the
value matched against `--cols`, and the key read off the row. The
`ColumnSpec` constructor throws `InvalidArgumentException` on a
mismatch:

```
ColumnSpec header 'Name' must equal key 'name'
```

Go cannot express the split through a `table:""` tag, so no SDK offers
one.

**4. Zero rows emits nothing** — not even a bare header row. Emptiness
is decided by row count, never column count, so a `ColumnSpec` list
never resurrects a header for an empty payload.

**5. `priority` is accepted, stored and ignored.** The hide-on-overflow
behavior it drives is implemented in Go only; the field is kept so specs
stay portable until that feature is ported.

`Dispatcher` collapses `--cols` and the `ColumnSpec` list into one
ordered list before calling a formatter
(`Projection::resolveEffectiveCols()`), so the `$cols` argument a
formatter receives is already final. `Formatter::render()` is unchanged:
third-party formatters pick up correct ordering with no code change.
That collapse is sound only because `header === key`.

### `--template`

The minimal renderer substitutes `{key}` per field and additionally
supports a `{*}` placeholder that expands to every resolved column's
value, tab-separated, in schema order:

```php
// template: '{*}'
// alpha<TAB>3<TAB>ready
// beta<TAB>8<TAB>held
```

`--template` and `--cols` are mutually exclusive, so on this path the
`ColumnSpec` list is the sole ordering signal. `{*}` is **PHP-specific
for now**: Go exposes `.Cols` and Python and TS expose `cols`, all
iterable column *names*, whereas `{*}` yields pre-joined row *values*.
The two are not the same affordance, and the house spelling for the
minimal-renderer tier is still an open decision.

### Go-vs-payload capability gaps

| Capability | Go (reference) | php | py / ts | rs |
|---|---|---|---|---|
| Column order source | `table:""` tags, declaration order | `ColumnSpec` list order | `ColumnSpec` list order | `ColumnSpec` list order |
| `priority` hide-on-overflow | implemented | accepted, stored, ignored | accepted, stored, ignored | accepted, stored, ignored |
| `header != key` | inexpressible via `table:""` | `InvalidArgumentException` | rejected at construction | panics in `ColumnSpec::new` |
| json/yaml key order | follows resolved order | follows resolved order | follows resolved order | follows resolved order |
| `--cols` reorders | yes | yes | yes | yes |
| Built-in formats | `table`, `json`, `yaml`, `csv`, `text` (+ `human`) | `table`, `json`, `yaml` | all five | `table`, `json`, `yaml` |
| Ordered columns on the template path | `.Cols` | `{*}` | `cols` | none |

Go's inability to express `header != key` is the *reason* rule 3 is
universal: no SDK may carry a capability the reference cannot mirror.

### Conformance status

PHP satisfies all five ordering rules on both the formatter and template
paths, as does Go across all five formats. The cross-runtime fixtures
under `sdk/tests/cross-lang/` execute the contract against every runtime.
Two gaps are open:

- **`csv` and `text` are not implemented** in PHP or Rust. Only `table`,
  `json` and `yaml` are portable across all five runtimes, so a caller
  writing against the kit output contract cannot assume `--format csv`
  exists. The fixtures record this as `rs-php-no-csv-text`.
- **rs has no ordered-column affordance on the `--template` path**,
  where PHP has `{*}`. The shared spelling for that tier is undecided.

The fixtures compare the **column order re-parsed from each runtime's own
output**, never raw bytes — PHP's YAML emits the dash on its own line and
Rust's table renderer pads cells, so byte comparison was never viable.
Byte-level formatting parity is pinned by each SDK's own unit tests
instead.

## MCP surface

Serves the Model Context Protocol over a bridged command tree, exposing one
MCP tool per runnable leaf. One mount answers **both** protocol revisions —
`2024-11-05` (handshake) and `2026-07-28` (stateless per-request envelope) —
choosing per request, because the newer revision has no handshake to
negotiate with.

Wire behaviour is pinned by the shared cross-language fixtures in
`sdk/tests/cross-lang/fixtures/mcp-wire.json`, compared as raw bytes.

### Hosting: PSR-15

The surface is exported as a PSR-15 `RequestHandlerInterface`, not a server.
Kit does not own your HTTP stack, so binding the handler is your decision;
being a plain request-to-response function also means the wire behaviour is
testable without opening a socket.

```php
<?php

use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Command;
use HopTop\Kit\Mcp\Mount;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\RequestHandler;
use HopTop\Kit\Mcp\Result;
use HopTop\Kit\Mcp\ServerInfo;
use Nyholm\Psr7\Factory\Psr17Factory;

$root = (new Command(name: 'app'))->addCommand(
    new Command(
        name: 'ping',
        description: 'Ping the server',
        annotations: ['kit/side-effect' => 'read'],
        runner: static fn (array $flags): Result => new Result(stdout: "pong\n"),
    ),
);

$factory = new Psr17Factory();

$handler = new RequestHandler(
    new Bridge($root, Policy::default()),
    new Mount(serverInfo: new ServerInfo('app', '1.0.0')),
    $factory,   // PSR-17 response factory
    $factory,   // PSR-17 stream factory
);
```

Mount it wherever your stack routes requests. With a PSR-15 middleware
pipeline (Slim, Mezzio, Laminas):

```php
$app->post('/mcp', $handler);
```

Under plain php-fpm, bridge the superglobals with any PSR-7 implementation
and emit the result:

```php
$request = Psr17Factory::fromGlobals();   // or your bridge of choice
$response = $handler->handle($request);

http_response_code($response->getStatusCode());
foreach ($response->getHeaders() as $name => $values) {
    header($name.': '.implode(', ', $values), true);
}
echo (string) $response->getBody();
```

Two details matter when choosing a PSR-7 bridge:

- **Preserve repeated headers.** The protocol tolerates a header sent twice
  with identical values but rejects conflicting duplicates, which a
  comma-joined `$_SERVER['HTTP_*']` value cannot express.
- **Do not re-encode the body.** Responses are already serialized; passing
  them through a JSON layer will reorder keys and break parity.

### Safety

Exposure is gated by `Policy::allowed()`. The default is deliberately
closed: **no remote surface may invoke a destructive leaf**, and a blocked
call comes back as an `isError` result at HTTP 200 rather than a transport
error — the call was understood and declined, not malformed.

```php
// Opt a surface in explicitly; the empty default means block-all.
new Policy(allowDestructiveOn: [Surface::Mcp]);
```

Leaves are classified from `kit/*` annotations: `kit/side-effect`
(`destructive`, `destructive-local`, `destructive-shared`),
`kit/auth-required`, and `kit/requires-confirmation`.

### Confirmation

A `kit/requires-confirmation` leaf requires an `X-Confirm-Token` header by
default. Supplying a `confirmationKey` instead opts into the 2026-07-28 MRTR
flow: the first call answers `resultType: "input_required"` with an
elicitation form and a signed `requestState`, and the client retries with
the user's decision. The token is bound to the tool, its arguments and the
caller, so the surface stays stateless and a token minted for one call
cannot be replayed against another.

```php
new Mount(confirmationKey: $key);   // HMAC key, never a user-supplied value
```

Clients that do not advertise form elicitation keep the header gate — the
spec forbids sending input requests to a client that cannot answer them.

### Protocol layer

The surface implements the JSON-RPC framing and era detection directly
rather than delegating to `mcp/sdk`. The published SDK (v0.7.1, the latest
release) carries revisions `2024-11-05` through `2025-11-25` only —
`2026-07-28`, `server/discover`, `resultType` and `cacheScope` exist on its
`main` branch but in no tagged release. Its supporting types also disagree
with this surface where the fixtures are explicit: `Mcp\Schema\JsonRpc\Error`
rejects a `null` id, which the `2024-11-05` era must round-trip verbatim,
and `ProtocolVersionMiddleware` answers an unknown version with its own
wording and a `2025-03-26` fallback rather than the `-32022` payload the
fixtures pin.

Revisit once a release carries the modern era: `ProtocolVersion`'s
handshake/modern split is the right vocabulary to adopt, and the middleware
already reads the reserved `_meta` protocol-version key.

### Scope

Deprecated upstream features (Roots, Sampling, Logging, HTTP+SSE) are
unimplemented, matching the Go reference. Pagination, `tasks/*` and
`subscriptions/listen` are likewise not served here.

### Cross-references

- [Serve MCP from any SDK](../guides/serve-mcp-from-any-sdk.md)
  — the polyglot adopter guide
- [Expose your CLI over MCP](../guides/expose-cli-over-mcp.md)
  — the Go reference surface, in depth

## Offline enforcement

The family-wide `--offline` global disables network access. It is the
highest-precedence override: per-command network opt-ins behave as if
their opt-out flag had been passed. It only forces opt-outs on — it never
un-sets an explicitly passed `--no-*` flag.

Loopback is exempt. `--offline` means "do not talk to the network", not
"do not talk to myself", so `127.0.0.1`, `localhost`, `[::1]` and unix
sockets stay reachable. Hostnames are remote even when they would resolve
to loopback, because performing that resolution is itself network access.

### Setting the marker

```php
use HopTop\Kit\Net\NetPolicy;

NetPolicy::setOffline(true);   // once, at start-up
NetPolicy::isOffline();        // consult
```

Go threads this through `context.Context`; PHP has no ambient context, so
the marker is process state. A CLI invocation is exactly one process, which
is the scope `--offline` describes.

### Enforcement

The marker alone is advisory — a caller who forgets to consult it still
reaches the wire. Enforcement therefore sits beneath the caller, and a
blocked request throws `OfflineException` rather than skipping silently.

For Guzzle, guard the **handler stack**:

```php
use HopTop\Kit\Net\OfflineGuard;

$client = new GuzzleHttp\Client(['handler' => OfflineGuard::stack()]);
// or, on a stack you already built:
OfflineGuard::push($stack);
```

The stack is the right seam because Guzzle's `request()`, `send()`,
`requestAsync()` and `sendAsync()` are siblings — none delegates to
another — but all four run the stack. A decorator wrapping only PSR-18's
`sendRequest()` would leave `request()` and `send()` unguarded.

For a non-Guzzle PSR-18 client, where `sendRequest()` is the only seam,
wrap the client:

```php
use HopTop\Kit\Net\OfflineGuardClient;

$client = OfflineGuardClient::wrap($psr18Client);
```

`OfflineException` implements PSR-18's `ClientExceptionInterface`, so
conforming callers catch it normally.

`ApiClient` installs the guard on the client it builds when you inject
none, so the default path is enforced without opting in. An injected
client is left alone — its owner chose its stack — so guard it yourself.

### Scope

Enforcement covers HTTP(S) through Guzzle and PSR-18, which is every
network client in this port today. It does **not** cover code that opens a
socket directly: `fsockopen`, `stream_socket_client`, PDO and other
database drivers, or `file_get_contents()` on an `http://` URL. For those
`--offline` stays advisory and the call site must consult
`NetPolicy::isOffline()` itself.

`Telemetry\Sink\HttpsSink` is deliberately **not** guarded. Telemetry is
logging-class egress: `--offline` stops traffic the user asked for, it is
not a second consent gate on diagnostics — the same way a remote syslog
target is not muted by an offline flag. Consent and telemetry mode already
govern whether anything is emitted at all. Do not pass it a guarded
client; that would suppress diagnostics `--offline` is not meant to touch.

### CLI flag

Not yet wired. This port has no global-flag layer — `src/Cli/Cli.php` is a
stub — so the marker is set programmatically today. Registering `--offline`
as a Symfony Console global belongs with the CLI-layer work, which will
call `NetPolicy::setOffline()` from the resolved flag.

## Telemetry

The PHP SDK ships a publish-only telemetry client under the
`HopTop\Kit\Telemetry` namespace. It mirrors the Go ground truth at
`go/runtime/telemetry/`.

### Default-denied posture

Telemetry is **off by default**. The PHP SDK never prompts the user; the
canonical consent prompt lives in the Go CLI. Adopters drive the lifecycle
with:

```
kit telemetry status      # show current mode + consent
kit telemetry enable      # opt in
kit telemetry disable     # opt out
kit telemetry reset       # clear persisted decision
```

The PHP SDK only **reads** the persisted decision from
`$XDG_CONFIG_HOME/kit/config.yaml` (default
`~/.config/kit/config.yaml`) at the `kit.telemetry.consent`
partition. A pre-refactor `$XDG_CONFIG_HOME/kit/telemetry.yaml`
(bare `telemetry.consent`) is honored as a read-only fallback.

### What is collected

| Mode  | Fields recorded                                                       |
|-------|-----------------------------------------------------------------------|
| `off` | nothing (no envelope created)                                         |
| `anon`| `event`, `ts`, `install_id`, `mode`, `sdk`                            |
| `full`| anon fields + `attrs` (redacted PII / token shapes)                   |

`install_id` is a SHA-256 hex digest of 32 random bytes stored at
`$XDG_STATE_HOME/kit/telemetry/installation_id`. Rotate via `kit telemetry reset`
or `InstallId::rotate()`.

### Disabling

Any of these turns telemetry off:

| Signal                           | Effect                                  |
|----------------------------------|-----------------------------------------|
| `DO_NOT_TRACK=1` (or any truthy) | Honored before mode resolution          |
| `KIT_TELEMETRY_MODE=off`         | Mode resolves to Off, all events drop   |
| `KIT_TELEMETRY_CONSENT=denied`   | Reserved override (Go CLI authoritative)|
| `kit telemetry disable`          | Persists `state: denied` in YAML        |

If the persisted YAML reports `state: denied`, `Telemetry::record()` short-
circuits regardless of mode.

### Sink selection

The transport is chosen via `KIT_TELEMETRY_SINK`:

| Value              | Sink                                                       |
|--------------------|------------------------------------------------------------|
| (unset) / `jsonl`  | `JsonlSink` — append JSONL to a per-PID file under XDG_STATE (default; FPM-safe) |
| `none`             | `NullSink` — drop every envelope (CI / staging)            |

`https` is **not** an accepted value of this variable. The facade never
constructs an HTTP client, so `KIT_TELEMETRY_SINK=https` reports a
diagnostic and falls back to `JsonlSink`. Adopters who need HTTPS
construct `HttpsSink` themselves and call `Telemetry::setSink()` (see
the FPM caveat below). Note this differs from the Python, TypeScript,
and Rust SDKs, where `https` *is* env-selectable.

Any other explicitly-set value (a typo such as `htpps`, or an
unimplemented transport) is likewise reported and falls back to
`JsonlSink` — an operator's choice is never discarded in silence.

Diagnostics go to a reporter that defaults to a no-op, so the SDK stays
quiet on import and never writes to a php-fpm response body. Wire it to
your logger to see them:

```php
Telemetry::setSinkErrReporter(static fn (string $m) => error_log($m));
```

**JsonlSink** registers a `register_shutdown_function` callback so envelopes
are flushed even when the caller never calls `Telemetry::flush()`. The on-
disk layout is `$XDG_STATE_HOME/kit/telemetry/inbox/php-<pid>.jsonl` with
LOCK_EX serialization and a 10 MiB size-rotation trigger. A separate Go drain
(future kit telemetry daemon) sweeps these files.

**HttpsSink** posts batched NDJSON to a remote ingestor. It is opt-in.

#### FPM caveat (HTTPS sink)

`HttpsSink::flush()` makes synchronous HTTPS calls. Under php-fpm a flush
during a request adds the round-trip to that request's wall-clock time. The
class deliberately does **not** auto-register a shutdown flush. In FPM the
recommended pattern is:

- Use the default `JsonlSink` so writes happen at shutdown after the
  response is sent.
- Or, if HTTPS is required, construct the sink yourself and only call
  `flush()` from a long-running worker — never from a request hot path.

CLI processes can safely register the shutdown flush themselves:

```php
register_shutdown_function([$httpsSink, 'flush']);
```

#### Client hardening (HTTPS sink)

`HttpsSink` does not construct its own Guzzle client — the adopter injects
one, so the adopter owns its security options. Kit sets only
`connect_timeout`, `timeout`, and `http_errors`; every other Guzzle default
applies as-is. Two defaults matter:

- **Redirects follow by default.** Kit never sets `allow_redirects`, so a
  `3xx` from the ingestor is followed automatically and the NDJSON batch is
  re-sent to whatever host the `Location` header names. Telemetry bodies
  carry `install_id` and, in `full` mode, redacted attrs.
- **Cookies are off by default.** Kit never configures a jar. Adding one
  puts the client in scope for cookie-domain-scoping advisories that
  otherwise cannot apply.

Kit performs no host allowlisting on the configured `$endpoint`. If the
endpoint comes from untrusted configuration, validate it before
constructing the sink. A hardened client:

```php
$client = new \GuzzleHttp\Client([
    'allow_redirects' => false,  // ingestor is a fixed endpoint; never chase 3xx
    'cookies'         => false,  // no jar: telemetry needs no session state
]);
$sink = new HttpsSink('https://ingest.example/v1/events', $client);
```

The same applies to `HopTop\Kit\Api\ApiClient`, which falls back to a
default-constructed `GuzzleHttp\Client` when no client is injected. Pass a
configured client when the `baseURL` is not a trusted constant.

### Redaction

`Redactor` applies best-effort PII / token-prefix replacement to all
attributes in Full mode:

- Email addresses → `<redacted:email>`
- IPv4 / IPv6 → `<redacted:ipv4>` / `<redacted:ipv6>`
- `$HOME` paths → `$HOME`
- Common token shapes (`sk-…`, `ghp_…` / `ghu_…` / `gho_…` / `ghs_…` /
  `ghr_…`, `xoxb-…`) → `<redacted:token>`

Adopters can supply a custom callback for project-specific patterns:

```php
use HopTop\Kit\Telemetry\Redactor;
use HopTop\Kit\Telemetry\Telemetry;

Telemetry::setRedactor(new Redactor(function (array $attrs): array {
    // project-specific scrubbing; runs after the default pass
    return $attrs;
}));
```

The custom callback's output is re-run through the default pass as defense
in depth.

### Bus transport

The PHP SDK is **publish-only**. It does not consume from any event bus.
Envelopes flow PHP → JSONL/HTTPS → Go drain → bus. Adopters who need bus
consumption should call the Go runtime.

### Cross-references

- `go/runtime/telemetry/` — canonical implementation

<!-- release: track hop-top/cite ^0.2.0 -->
