<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The binding a dispatcher serves.
 *
 * `Http` (Streamable HTTP) mirrors the method, tool name and protocol
 * version into headers and requires them to agree with the body (V4,
 * V6, V7). `Stdio` has no header layer, so the body is the only source
 * of truth and those three checks do not apply; every other check runs
 * unchanged.
 */
enum Transport
{
    case Http;
    case Stdio;
}
