<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/** Protocol constants shared by the dispatcher and both era handlers. */
final class Protocol
{
    public const LEGACY_VERSION = '2024-11-05';
    public const MODERN_VERSION = '2026-07-28';

    public const HEADER_PROTOCOL_VERSION = 'MCP-Protocol-Version';
    public const HEADER_METHOD = 'Mcp-Method';
    public const HEADER_NAME = 'Mcp-Name';

    public const META_PROTOCOL_VERSION = 'io.modelcontextprotocol/protocolVersion';
    public const META_CLIENT_INFO = 'io.modelcontextprotocol/clientInfo';
    public const META_CLIENT_CAPABILITIES = 'io.modelcontextprotocol/clientCapabilities';
    public const META_SERVER_INFO = 'io.modelcontextprotocol/serverInfo';

    public const RESULT_TYPE_COMPLETE = 'complete';
    public const RESULT_TYPE_INPUT_REQUIRED = 'input_required';

    public const SENTINEL_PREFIX = '=?base64?';
    public const SENTINEL_SUFFIX = '?=';
}
