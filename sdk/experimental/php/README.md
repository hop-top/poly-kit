# php

experimental PHP client SDK.

## Modules

- [`src/Id/`](src/Id/) — TypeID primitive (cross-language; see
  [ADR 0001](../../../docs/adr/0001-typeid-primitive.md))

## URI facade

The experimental SDK exposes a thin facade over `hop-top/uri` so kit callers can
use the shared URI contract without depending on kit-specific parsing code.

```php
<?php

use Hop\Uri\ActionRoute;
use Hop\Uri\Policy;
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

This facade intentionally delegates to `hop-top/uri`; it does not reimplement
URI parsing, vanity handling, action routing, or handler identity.

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
| `https`            | Adopters construct `HttpsSink` themselves and call `Telemetry::setSink()` (see FPM caveat below) |

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

* Use the default `JsonlSink` so writes happen at shutdown after the
  response is sent.
* Or, if HTTPS is required, construct the sink yourself and only call
  `flush()` from a long-running worker — never from a request hot path.

CLI processes can safely register the shutdown flush themselves:

```php
register_shutdown_function([$httpsSink, 'flush']);
```

### Redaction

`Redactor` applies best-effort PII / token-prefix replacement to all
attributes in Full mode:

* Email addresses → `<redacted:email>`
* IPv4 / IPv6 → `<redacted:ipv4>` / `<redacted:ipv6>`
* `$HOME` paths → `$HOME`
* Common token shapes (`sk-…`, `ghp_…` / `ghu_…` / `gho_…` / `ghs_…` /
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

* `go/runtime/telemetry/` — canonical implementation
