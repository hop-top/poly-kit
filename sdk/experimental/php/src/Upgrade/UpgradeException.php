<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use RuntimeException;

/**
 * Base exception for the kit Upgrade namespace.
 *
 * Every failure the namespace raises on purpose extends this class, so a
 * caller can catch the whole family with one `catch (UpgradeException $e)`.
 */
class UpgradeException extends RuntimeException {}
