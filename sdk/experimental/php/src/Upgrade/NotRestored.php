<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * A rollback that could not undo a failed run: a `down` was missing or
 * failed, or the newest step's `up` failed (it cannot be downed), and no
 * backup could restore the schema, so the stored version stays at the last
 * step still applied. It sits in the exception chain of the run's
 * `MigrateException` (`UpgradeErrors::isNotRestored()`); its previous
 * exception is the cause, typically `BackupUnsupported`. No `reverted` event
 * is published for such a rollback.
 */
final class NotRestored extends UpgradeException {}
