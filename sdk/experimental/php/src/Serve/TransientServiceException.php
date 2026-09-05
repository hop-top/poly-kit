<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

use RuntimeException;

/**
 * A service failure a retry may clear.
 *
 * The contract keeps the TRANSIENT propagation rule inside the serve
 * taxonomy: "a failure wrapping a transient error keeps exit 6 — so an
 * agent's retry branch behaves the same whichever language the tool it
 * is driving was written in". A service throws this instead of a plain
 * exception when the failure is an upstream blip rather than a
 * misconfiguration, and the supervisor propagates exit 6 unchanged
 * rather than flattening it to the generic 1.
 */
final class TransientServiceException extends RuntimeException
{
}
