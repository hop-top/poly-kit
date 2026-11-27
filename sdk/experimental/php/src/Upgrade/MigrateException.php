<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use Throwable;

/**
 * What `Migrator::run()` and `rollbackLatest()` throw for one schema. The
 * message is the contract text (`migrate db: up 1.2.0 failed: boom`); the
 * previous chain ends in the original exception. A rollback that failed on
 * top of the step is in `$rollback`. Use `UpgradeErrors` to ask what is in
 * the chain; do not match on the message.
 */
final class MigrateException extends UpgradeException
{
    public function __construct(
        string $message,
        public readonly string $schema = '',
        ?Throwable $previous = null,
        public readonly ?Throwable $rollback = null,
    ) {
        parent::__construct($message, 0, $previous);
    }
}
