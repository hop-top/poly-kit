<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * JSON-RPC and MCP error codes used by both eras.
 *
 * `-32020`..`-32022` are the codes 2026-07-28 reserves; the rest are base
 * JSON-RPC.
 */
final class ErrorCodes
{
    public const PARSE = -32700;
    public const INVALID_REQUEST = -32600;
    public const METHOD_NOT_FOUND = -32601;
    public const INVALID_PARAMS = -32602;
    public const INTERNAL = -32603;

    public const HEADER_MISMATCH = -32020;
    public const MISSING_CLIENT_CAPABILITY = -32021;
    public const UNSUPPORTED_VERSION = -32022;
}
