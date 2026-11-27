<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

use Closure;
use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Upgrade\CanceledException;
use HopTop\Kit\Upgrade\Locker;

/** A recording driver that implements `Locker`. */
class LockingDriver extends RecordingDriver implements Locker
{
    public function lock(?Cancellation $cancel = null): Closure
    {
        ($this->record)($this->name() . ' lock');
        if ($this->dc['lock_blocked'] ?? false) {
            if ($cancel !== null && $cancel->cancelled()) {
                throw new CanceledException();
            }
            throw new \RuntimeException('lock would block forever: the step must set canceled');
        }
        self::failIf($this->dc['lock_fail'] ?? '');
        if (($this->dc['lock_sets_version'] ?? '') !== '') {
            $this->dc['version'] = $this->dc['lock_sets_version'];
        }

        return function (): void {
            ($this->record)($this->name() . ' unlock');
            self::failIf($this->dc['unlock_fail'] ?? '');
        };
    }
}
