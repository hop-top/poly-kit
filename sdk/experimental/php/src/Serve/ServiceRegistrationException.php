<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

use RuntimeException;

/**
 * Thrown when a registration is rejected at construction time.
 *
 * A duplicate name, an invalid one, or a dependency cycle is a wiring
 * bug in the tool's entry point, not a runtime condition: it surfaces
 * on the first run rather than at the first `serve`, and there is no
 * last-writer-wins path. Go panics; throwing is this port's
 * equivalent. What the contract forbids is letting execution survive
 * it as a warning.
 */
final class ServiceRegistrationException extends RuntimeException
{
}
