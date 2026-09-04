<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The 2024-11-05 handler: `initialize`, `tools/list`, `tools/call`.
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
    public function __construct(
        private Bridge $bridge,
        private ServerInfo $serverInfo = new ServerInfo(),
    ) {
    }

    public function handle(Request $request): Response
    {
        if ('' !== $request->jsonrpc && '2.0' !== $request->jsonrpc) {
            return Response::error($request, ErrorCodes::INVALID_REQUEST, 'invalid jsonrpc version', 400);
        }

        return match ($request->method) {
            'initialize' => $this->initialize($request),
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

            $tools[] = ToolEnvelope::build($leaf);
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

        $leaf = $this->bridge->resolveLeaf($name);
        if (null === $leaf || !$leaf->enabledOn(Surface::Mcp)) {
            return Response::error($request, ErrorCodes::INVALID_PARAMS, 'unknown tool: '.$name, 200);
        }

        // Gating is mirrored onto the result envelope so MCP-aware clients
        // see isError while HTTP-only clients see the matching status.
        if ($leaf->class->authRequired && '' === $request->header('Authorization')) {
            return Response::result($request, CallResult::errorBlock('authentication required'), 401);
        }

        if ($leaf->class->requiresConfirmation && '' === $request->header('X-Confirm-Token')) {
            return Response::result($request, CallResult::errorBlock('confirmation required'), 428);
        }

        $arguments = \is_array($params['arguments'] ?? null) ? $params['arguments'] : [];

        try {
            $result = $this->bridge->invoke($name, $arguments, Surface::Mcp);
        } catch (UnknownCommandException|SurfaceNotEnabledException) {
            return Response::error($request, ErrorCodes::INVALID_PARAMS, 'unknown tool: '.$name, 200);
        } catch (BridgeException $e) {
            // Destructive blocks and runner failures alike surface as an
            // isError result at 200, never as a transport error.
            return Response::result($request, CallResult::errorBlock($e->getMessage()), 200);
        }

        return Response::result($request, CallResult::render($result));
    }
}
