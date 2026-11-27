<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use Throwable;

/**
 * What a driver's `backup` or `restore` throws when kit cannot back up or
 * restore its store. Detect it anywhere in a run's exception with
 * `UpgradeErrors::isBackupUnsupported()`.
 */
final class BackupUnsupported extends UpgradeException
{
    public function __construct(string $message = 'upgrade: backup unsupported', int $code = 0, ?Throwable $previous = null)
    {
        parent::__construct($message, $code, $previous);
    }
}
