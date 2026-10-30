<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The 2024-11-05 handler: `initialize`, `ping`, `tools/list`, `tools/call`.
 * A notification (no `id` member) is never answered: 202, empty body.
 *
 * Ports Go's `mcpHandler`. This module is deliberately additive-only —
 * the dual-spec design preserves the legacy era byte-for-byte, and
 * keeping it in its own class is what makes that invariant checkable by
 * review: a modern-era change that touches this file is a bug.
 *
 * Application-level failures ride HTTP 200 here, matching the era's
 * convention; only a parse failure is a 400, and the auth/confirmation
 * refusals carry their own statuses.
 */
final readonly class LegacyHandler
{
    /**
     * @param (\Closure(Request): ?Identity)|null $verifier          establishes the caller of each `tools/call`
     * @param string                              $toolNameSeparator joins a leaf path into its tool name
     * @param Auditor                             $auditor           observes each refusal before it is answered
     */
    public function __construct(
        private Bridge $bridge,
        private ServerInfo $serverInfo = new ServerInfo(),
        private ?\Closure $verifier = null,
        private string $toolNameSeparator = Leaf::DEFAULT_TOOL_NAME_SEPARATOR,
        private Auditor $auditor = new Auditor(),
    ) {
    }

    public function handle(Request $request): Response
    {
        if ('' !== $request->jsonrpc && '2.0' !== $request->jsonrpc) {
            return Response::error($request, ErrorCodes::INVALID_REQUEST, 'invalid jsonrpc version', 400);
        }

        // No id member: a notification. JSON-RPC 2.0 forbids any reply, so
        // it is acknowledged with HTTP 202 and an empty body and is not
        // processed (the modern handler's V2 answer). An explicit
        // `"id": null` is a request and is answered.
        if (!$request->hasId()) {
            return Response::accepted();
        }

        return match ($request->method) {
            'initialize' => $this->initialize($request),
            // 2024-11-05 utility: the receiver MUST answer with an empty
            // result. Legacy era only; 2026-07-28 removed ping.
            'ping' => Response::result($request, new \stdClass()),
            'tools/list' => $this->toolsList($request),
            'tools/call' => $this->toolsCall($request),
            default => Response::error(
                $request,
                ErrorCodes::METHOD_NOT_FOUND,
                'method not found: '.$request->method,
                200,
            ),
        };
    }

    /**
     * The handshake. Params are ignored entirely — including `_meta`,
     * which is why a legacy client's `progressToken` changes nothing.
     */
    private function initialize(Request $request): Response
    {
        return Response::result($request, [
            'protocolVersion' => Protocol::LEGACY_VERSION,
            'capabilities' => ['tools' => new \stdClass()],
            'serverInfo' => $this->serverInfo->toArray(),
        ]);
    }

    private function toolsList(Request $request): Response
    {
        return Response::result($request, ['tools' => $this->tools()]);
    }

    /** @return list<array<string, mixed>> */
    private function tools(): array
    {
        $tools = [];

        foreach ($this->bridge->leaves() as $leaf) {
            if (!$leaf->enabledOn(Surface::Mcp)) {
                continue;
            }

            $tools[] = ToolEnvelope::build($leaf, $this->toolNameSeparator);
        }

        return $tools;
    }

    private function toolsCall(Request $request): Response
    {
        $params = $request->params ?? [];
        $name = \is_string($params['name'] ?? null) ? $params['name'] : '';

        if ('' === $name) {
            return Response::error($request, ErrorCodes::INVALID_PARAMS, 'missing tool name', 200);
        }

        $leaf = $this->bridge->resolveLeaf($name, $this->toolNameSeparator);
        if (null === $leaf || !$leaf->enabledOn(Surface::Mcp)) {
            return Response::error($request, ErrorCodes::INVALID_PARAMS, 'unknown tool: '.$name, 200);
        }

        // Gating is mirrored onto the result envelope so MCP-aware clients
        // see isError while HTTP-only clients see the matching status.
        // Only the mount's verifier authenticates: a bare Authorization
        // header does not.
        $identity = Identity::establish($this->verifier, $request);
        if ($leaf->class->authRequired && null === $identity) {
            $this->auditor->refused($leaf, SpecVersion::Legacy, AuditRecord::ERR_AUTH_REFUSED);

            return Response::result($request, CallResult::errorBlock('authentication required'), 401)
                ->unauthenticated();
        }

        if ($leaf->class->requiresConfirmation && '' === $request->header('X-Confirm-Token')) {
            $this->auditor->refused($leaf, SpecVersion::Legacy, AuditRecord::ERR_CONFIRMATION_REQUIRED);

            return Response::result($request, CallResult::errorBlock('confirmation required'), 428);
        }

        $arguments = \is_array($params['arguments'] ?? null) ? $params['arguments'] : [];

        try {
            $result = $this->bridge->invoke($name, $arguments, Surface::Mcp, $this->toolNameSeparator);
        } catch (UnknownCommandException|SurfaceNotEnabledException) {
            return Response::error($request, ErrorCodes::INVALID_PARAMS, 'unknown tool: '.$name, 200);
        } catch (DestructiveBlockedException $e) {
            // A destructive block surfaces as an isError result at 200,
            // never as a transport error; it is audited first, as the Go
            // bridge audits every refusal before answering.
            $this->auditor->refused($leaf, SpecVersion::Legacy, AuditRecord::ERR_DESTRUCTIVE_BLOCKED);

            return Response::result($request, CallResult::errorBlock($e->getMessage()), 200);
        }

        return Response::result($request, CallResult::render($result));
    }
}
