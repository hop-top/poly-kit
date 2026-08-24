<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The 2026-07-28 handler: `server/discover`, `tools/list`, `tools/call`.
 *
 * Ports Go's `mcpModernHandler`. The era is stateless — no handshake, no
 * session — so every request re-declares its protocol version, client
 * capabilities and routing headers, and the validation chain V1..V9 runs
 * in a fixed order with the first failure answering.
 *
 * HTTP status is 400/404 only where the spec mandates it; application
 * errors ride 200, matching the legacy convention.
 */
final readonly class ModernHandler
{
    /**
     * @param list<string> $originAllowlist
     */
    public function __construct(
        private Bridge $bridge,
        private ServerInfo $serverInfo = new ServerInfo(),
        private CacheHints $cacheHints = new CacheHints(),
        private array $originAllowlist = [],
        private ConfirmationGate $confirmationGate = new HeaderConfirmationGate(),
    ) {
    }

    public function handle(Request $request): Response
    {
        if (!$this->originAllowed($request)) {
            return Response::error($request, ErrorCodes::INVALID_REQUEST, 'origin not allowed', 403);
        }

        // V1 — jsonrpc member absent or "2.0", the same tolerance as legacy.
        if ('' !== $request->jsonrpc && '2.0' !== $request->jsonrpc) {
            return $this->fail($request, ErrorCodes::INVALID_REQUEST, 'invalid jsonrpc version', 400);
        }

        // V2 — an absent id makes this a notification: acknowledged and
        // discarded without processing.
        if (!$request->hasId()) {
            return Response::accepted();
        }

        if (!self::validRequestId((string) $request->rawId)) {
            return $this->fail(
                $request,
                ErrorCodes::INVALID_REQUEST,
                'invalid request id: must be a string or integer, got '.$request->rawId,
                400,
            );
        }

        // V3 — the reserved _meta keys.
        $meta = RequestMeta::parse($request->params);
        if ($meta instanceof CheckError) {
            return $this->fail($request, $meta->code, $meta->message, $meta->status, $meta->data);
        }

        // V4 — MCP-Protocol-Version header present and equal to _meta.
        [$header, $ok] = $request->singleHeaderValue(Protocol::HEADER_PROTOCOL_VERSION);
        if (!$ok) {
            return $this->fail(
                $request,
                ErrorCodes::HEADER_MISMATCH,
                Protocol::HEADER_PROTOCOL_VERSION.' header sent with conflicting duplicate values',
                400,
            );
        }

        if ('' === $header) {
            return $this->fail(
                $request,
                ErrorCodes::HEADER_MISMATCH,
                'missing '.Protocol::HEADER_PROTOCOL_VERSION.' header',
                400,
            );
        }

        // A non-string _meta value can never equal the header string.
        if (!$meta->versionIsText || $header !== $meta->version) {
            return $this->fail(
                $request,
                ErrorCodes::HEADER_MISMATCH,
                \sprintf(
                    '%s header %s does not match _meta protocolVersion %s',
                    Protocol::HEADER_PROTOCOL_VERSION,
                    GoFormat::quote($header),
                    GoFormat::value($meta->versionRaw),
                ),
                400,
            );
        }

        // V5 — the requested version must be the one this handler speaks.
        // The supported list excludes 2024-11-05 on purpose: that era is
        // reachable only through its handshake, not per-request selection.
        if (Protocol::MODERN_VERSION !== $meta->version) {
            return $this->fail(
                $request,
                ErrorCodes::UNSUPPORTED_VERSION,
                'unsupported protocol version: '.$meta->version,
                400,
                ['supported' => [Protocol::MODERN_VERSION], 'requested' => $meta->versionRaw],
            );
        }

        // V6 — Mcp-Method header agreement.
        $methodError = $this->validateMethodHeader($request);
        if (null !== $methodError) {
            return $this->fail($request, $methodError->code, $methodError->message, $methodError->status);
        }

        // V8 — routing. V7 and V9 run inside tools/call.
        return match ($request->method) {
            'server/discover' => $this->discover($request),
            'tools/list' => $this->toolsList($request),
            'tools/call' => $this->toolsCall($request, $meta),
            default => $this->fail(
                $request,
                ErrorCodes::METHOD_NOT_FOUND,
                'method not found: '.$request->method,
                404,
            ),
        };
    }

    /**
     * Emits a modern error, naming the supported versions when the body
     * method is `initialize`.
     *
     * A legacy client reaching the modern handler has no fall-forward
     * mechanism, so the spec asks that every error it receives says which
     * versions the server speaks.
     */
    private function fail(Request $request, int $code, string $message, int $status, mixed $data = null): Response
    {
        if ('initialize' === $request->method) {
            $message .= '; supported protocol versions: '.Protocol::MODERN_VERSION;
        }

        return Response::error($request, $code, $message, $status, $data);
    }

    /**
     * V2's id rule: a JSON string, or a JSON number with no fractional
     * part. Base JSON-RPC also permits null, but this revision forbids it,
     * so null, booleans, floats, objects and arrays are all malformed.
     */
    private static function validRequestId(string $rawId): bool
    {
        if ('null' === $rawId) {
            return false;
        }

        $decoded = Json::decode($rawId);

        if (\is_string($decoded)) {
            return true;
        }

        if (\is_int($decoded)) {
            return true;
        }

        return \is_float($decoded) && $decoded === floor($decoded) && is_finite($decoded);
    }

    private function validateMethodHeader(Request $request): ?CheckError
    {
        [$header, $ok] = $request->singleHeaderValue(Protocol::HEADER_METHOD);

        if (!$ok) {
            return new CheckError(
                ErrorCodes::HEADER_MISMATCH,
                Protocol::HEADER_METHOD.' header sent with conflicting duplicate values',
                400,
            );
        }

        if ('' === $header) {
            return new CheckError(
                ErrorCodes::HEADER_MISMATCH,
                'missing '.Protocol::HEADER_METHOD.' header',
                400,
            );
        }

        if ($header !== $request->method) {
            return new CheckError(
                ErrorCodes::HEADER_MISMATCH,
                \sprintf(
                    '%s header %s does not match body method %s',
                    Protocol::HEADER_METHOD,
                    GoFormat::quote($header),
                    GoFormat::quote($request->method),
                ),
                400,
            );
        }

        return null;
    }

    /**
     * The mandatory modern discovery method. No listChanged flag
     * (notifications are unimplemented), no extensions map, no
     * instructions.
     */
    private function discover(Request $request): Response
    {
        $result = [
            'supportedVersions' => [Protocol::MODERN_VERSION],
            'capabilities' => ['tools' => new \stdClass()],
        ];

        return Response::result($request, $this->stamp($this->withCacheHints($result)));
    }

    private function toolsList(Request $request): Response
    {
        $tools = [];

        foreach ($this->bridge->leaves() as $leaf) {
            if (!$leaf->enabledOn(Surface::Mcp)) {
                continue;
            }

            $tools[] = ToolEnvelope::build($leaf);
        }

        return Response::result($request, $this->stamp($this->withCacheHints(['tools' => $tools])));
    }

    private function toolsCall(Request $request, RequestMeta $meta): Response
    {
        // V7 — the Mcp-Name header is the routing signal a gateway trusts
        // without parsing the body, so it is validated before any
        // body-shape check.
        $nameError = $this->validateNameHeader($request);
        if (null !== $nameError) {
            return $this->fail($request, $nameError->code, $nameError->message, $nameError->status);
        }

        $params = $request->params ?? [];
        $name = \is_string($params['name'] ?? null) ? $params['name'] : '';

        // V9 — reachable only defensively: V7 already rejects every
        // conforming request that would land here.
        if ('' === $name) {
            return Response::error($request, ErrorCodes::INVALID_PARAMS, 'missing tool name', 200);
        }

        $leaf = $this->bridge->resolveLeaf($name);
        if (null === $leaf || !$leaf->enabledOn(Surface::Mcp)) {
            return Response::error($request, ErrorCodes::INVALID_PARAMS, 'unknown tool: '.$name, 200);
        }

        if ($leaf->class->authRequired && '' === $request->header('Authorization')) {
            return Response::result($request, $this->stamp(CallResult::errorBlock('authentication required')), 401);
        }

        $refusal = $this->confirmationGate->check($leaf, $params, $request);
        if (null !== $refusal) {
            [$envelope, $status] = $refusal;

            return Response::result($request, $this->stamp($envelope), $status);
        }

        $arguments = \is_array($params['arguments'] ?? null) ? $params['arguments'] : [];

        try {
            $result = $this->bridge->invoke($name, $arguments, Surface::Mcp);
        } catch (UnknownCommandException|SurfaceNotEnabledException) {
            return Response::error($request, ErrorCodes::INVALID_PARAMS, 'unknown tool: '.$name, 200);
        } catch (BridgeException $e) {
            return Response::result($request, $this->stamp(CallResult::errorBlock($e->getMessage())), 200);
        }

        $envelope = CallResult::render($result);

        if (null !== $result->data) {
            $envelope['structuredContent'] = $result->data;
        }

        return Response::result($request, $this->stamp($envelope));
    }

    private function validateNameHeader(Request $request): ?CheckError
    {
        [$header, $ok] = $request->singleHeaderValue(Protocol::HEADER_NAME);

        if (!$ok) {
            return new CheckError(
                ErrorCodes::HEADER_MISMATCH,
                Protocol::HEADER_NAME.' header sent with conflicting duplicate values',
                400,
            );
        }

        if ('' === $header) {
            return new CheckError(
                ErrorCodes::HEADER_MISMATCH,
                'missing '.Protocol::HEADER_NAME.' header',
                400,
            );
        }

        $decoded = Sentinel::decode($header);
        if (null === $decoded) {
            return new CheckError(
                ErrorCodes::HEADER_MISMATCH,
                Protocol::HEADER_NAME.' header value is not valid base64-sentinel encoded',
                400,
            );
        }

        if ('' === $decoded) {
            return new CheckError(
                ErrorCodes::HEADER_MISMATCH,
                Protocol::HEADER_NAME.' header decodes to an empty value',
                400,
            );
        }

        $params = $request->params ?? [];

        if (!\array_key_exists('name', $params)) {
            return new CheckError(
                ErrorCodes::HEADER_MISMATCH,
                Protocol::HEADER_NAME.' header present but body params.name is absent',
                400,
            );
        }

        $rawName = $params['name'];

        // A non-string name is left to V9's params decode rather than
        // reported as a header mismatch.
        if (!\is_string($rawName)) {
            return null;
        }

        if ($decoded !== $rawName) {
            return new CheckError(
                ErrorCodes::HEADER_MISMATCH,
                \sprintf(
                    '%s header %s does not match body params.name %s',
                    Protocol::HEADER_NAME,
                    GoFormat::quote($decoded),
                    GoFormat::quote($rawName),
                ),
                400,
            );
        }

        return null;
    }

    /**
     * Adds the members every modern result carries: `resultType` and a
     * result-level `_meta` naming the server.
     *
     * A producer that already chose a `resultType` keeps it — the MRTR
     * confirmation gate is the only one that does.
     *
     * @param array<string, mixed> $result
     *
     * @return array<string, mixed>
     */
    private function stamp(array $result): array
    {
        $result['resultType'] ??= Protocol::RESULT_TYPE_COMPLETE;
        $result['_meta'] = [Protocol::META_SERVER_INFO => $this->serverInfo->toArray()];

        return $result;
    }

    /**
     * Cache hints ride the cacheable list results only — `tools/call` is
     * not a cacheable operation and never carries them.
     *
     * @param array<string, mixed> $result
     *
     * @return array<string, mixed>
     */
    private function withCacheHints(array $result): array
    {
        $result['ttlMs'] = $this->cacheHints->ttlMs;
        $result['cacheScope'] = $this->cacheHints->cacheScope;

        return $result;
    }

    /**
     * The opt-in Origin allowlist. Unset means no check — that is the
     * deployment proxy's responsibility — and a request without an Origin
     * is never refused.
     */
    private function originAllowed(Request $request): bool
    {
        if ([] === $this->originAllowlist) {
            return true;
        }

        $origin = $request->header('Origin');

        if ('' === $origin) {
            return true;
        }

        return \in_array($origin, $this->originAllowlist, true);
    }
}
