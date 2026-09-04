<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The policy gate refused a destructive leaf on a remote surface.
 *
 * Rendered as an `isError` result at HTTP 200, never as a transport
 * error: the call was understood and declined, not malformed.
 */
final class DestructiveBlockedException extends BridgeException
{
}
