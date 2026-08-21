<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Routes each request to the era that must serve it.
 *
 * Ports Go's `mcpDispatcher`. One mount serves both revisions, and the
 * era is decided per request rather than per connection, because the
 * 2026-07-28 revision has no handshake to negotiate with.
 *
 * Detection follows ADR 0042's normative rules. Two signals are
 * deliberately *not* markers, and both matter:
 *
 *  - A bare `params._meta` does not imply the modern era — 2024-11-05
 *    clients legitimately send `_meta.progressToken` and OTel headers.
 *    Only the reserved `protocolVersion` key counts.
 *  - The `MCP-Protocol-Version` header does not either. It predates
 *    2026-07-28, so a client that negotiated down to legacy sends it on
 *    every subsequent request; treating it as modern would serve that
 *    client's handshake and then brick its session.
 */
final readonly class Dispatcher
{
    public function __construct(
        private LegacyHandler $legacy,
        private ModernHandler $modern,
        private bool $legacyEnabled = true,
        private bool $modernEnabled = true,
    ) {
    }

    /**
     * D1: read and decode the body once, then classify.
     *
     * @param array<string, list<string>> $headers
     */
    public function dispatch(string $body, array $headers): Response
    {
        $decoded = Json::decode($body);

        // D1 — an unparseable body answers identically in both eras,
        // whatever headers came with it.
        if (!\is_array($decoded)) {
            return Response::error(
                null,
                ErrorCodes::PARSE,
                'parse error: '.JsonSyntaxError::describe($body),
                400,
            );
        }

        $request = self::toRequest($decoded, $headers);

        if (!$this->modernEnabled) {
            return $this->legacy->handle($request);
        }

        if (!$this->legacyEnabled) {
            return $this->modern->handle($request);
        }

        return Era::Modern === self::detectEra($request)
            ? $this->modern->handle($request)
            : $this->legacy->handle($request);
    }

    /**
     * Applies the precedence chain D2..D4.
     *
     * Incomplete or contradictory modern requests are never demoted to
     * legacy: a dual-era client relies on a recognizably modern error to
     * tell that it is talking to a modern server, so the modern handler
     * rejects them with modern codes instead.
     */
    public static function detectEra(Request $request): Era
    {
        // D2 — initialize is legacy unconditionally, even alongside modern
        // markers. A confused client then gets a working handshake, the
        // most recoverable outcome; modern clients never send it.
        if ('initialize' === $request->method) {
            return Era::Legacy;
        }

        // M4 — server/discover exists only in the modern era.
        if ('server/discover' === $request->method) {
            return Era::Modern;
        }

        // M1 / M2 — routing headers, read as first-value-only: a duplicate
        // still routes modern, and the conflict surfaces later in V4/V6/V7.
        if ('' !== $request->header(Protocol::HEADER_METHOD)) {
            return Era::Modern;
        }

        if ('' !== $request->header(Protocol::HEADER_NAME)) {
            return Era::Modern;
        }

        // M3 — the reserved _meta key, by presence alone; its value is not
        // inspected at detection time.
        if (self::hasModernMetaMarker($request->params)) {
            return Era::Modern;
        }

        // D4 — no markers: the byte-for-byte preservation path.
        return Era::Legacy;
    }

    /** @param array<string, mixed>|null $params */
    private static function hasModernMetaMarker(?array $params): bool
    {
        if (null === $params) {
            return false;
        }

        $meta = $params['_meta'] ?? null;

        return \is_array($meta) && \array_key_exists(Protocol::META_PROTOCOL_VERSION, $meta);
    }

    /**
     * @param array<array-key, mixed>     $decoded
     * @param array<string, list<string>> $headers
     */
    private static function toRequest(array $decoded, array $headers): Request
    {
        // The id is kept as raw JSON so it round-trips verbatim: an
        // explicit null must come back as null, while an absent id must
        // stay absent.
        $rawId = \array_key_exists('id', $decoded)
            ? Json::encode($decoded['id'])
            : null;

        return new Request(
            jsonrpc: \is_string($decoded['jsonrpc'] ?? null) ? $decoded['jsonrpc'] : '',
            method: \is_string($decoded['method'] ?? null) ? $decoded['method'] : '',
            params: \is_array($decoded['params'] ?? null) ? $decoded['params'] : null,
            rawId: $rawId,
            headers: $headers,
        );
    }
}
