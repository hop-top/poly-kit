<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * Base of the document migrator's failures. A failed verify is not one of
 * them: it is a `VerifyException`.
 */
abstract class DocException extends UpgradeException {}
