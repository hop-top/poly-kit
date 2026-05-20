<?php

declare(strict_types=1);

namespace HopTop\Kit\Id;

use RuntimeException;

/**
 * Base exception for the kit Id primitive.
 *
 * All Id-related failures (invalid prefix, invalid suffix, prefix mismatch
 * on typed parse) extend this class so callers can catch the whole family
 * with one `catch (IdException $e)`.
 */
class IdException extends RuntimeException {}
