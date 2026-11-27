<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use Closure;
use HopTop\Kit\Serve\Cancellation;

/**
 * Optional capability of a driver whose store several processes can migrate
 * at once. The migrator calls `lock` before it reads the driver's version and
 * holds the lock for the whole run of that schema (version read, baseline,
 * backup, every `up`, every version record, rollback, prune), then calls the
 * returned unlock.
 *
 * `lock` blocks until the lock is acquired or `$cancel` is cancelled; in the
 * second case it throws `CanceledException` and no lock is held. A `lock`
 * exception fails the schema before anything else. An unlock exception is
 * reported as a warning, never thrown. A driver that is not a `Locker` runs
 * unlocked.
 */
interface Locker
{
    /**
     * @return Closure(): void the unlock; throws when releasing fails
     * @throws \Throwable when the lock cannot be taken
     */
    public function lock(?Cancellation $cancel = null): Closure;
}
