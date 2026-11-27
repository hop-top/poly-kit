<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use Throwable;

/**
 * Asks what a migrator exception carries, the way Go uses `errors.Is`: the
 * whole `getPrevious()` chain is searched, and so is the failed rollback a
 * `MigrateException` holds.
 */
final class UpgradeErrors
{
    /** A rollback restored nothing (`NotRestored`). */
    public static function isNotRestored(Throwable $e): bool
    {
        return self::has($e, NotRestored::class);
    }

    /** A driver said it cannot back up or restore (`BackupUnsupported`). */
    public static function isBackupUnsupported(Throwable $e): bool
    {
        return self::has($e, BackupUnsupported::class);
    }

    /** A migration's or the final verify failed (`VerifyException`). */
    public static function isVerify(Throwable $e): bool
    {
        return self::has($e, VerifyException::class);
    }

    /** The run's `Cancellation` was cancelled (`CanceledException`). */
    public static function isCanceled(Throwable $e): bool
    {
        return self::has($e, CanceledException::class);
    }

    /** @param class-string<Throwable> $class */
    private static function has(Throwable $e, string $class): bool
    {
        $todo = [$e];
        while ($todo !== []) {
            $cur = array_pop($todo);
            if ($cur instanceof $class) {
                return true;
            }
            if ($cur->getPrevious() !== null) {
                $todo[] = $cur->getPrevious();
            }
            if ($cur instanceof MigrateException && $cur->rollback !== null) {
                $todo[] = $cur->rollback;
            }
        }

        return false;
    }
}
