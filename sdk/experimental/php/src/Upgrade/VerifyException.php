<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * A verify check failed: a `DocStep` verify here, a migration's verify in
 * the migrator. The message is the step's own text; `getPrevious()` is the
 * check's exception.
 */
final class VerifyException extends UpgradeException {}
