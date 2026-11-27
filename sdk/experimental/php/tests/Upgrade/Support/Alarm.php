<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

use Closure;
use HopTop\Kit\Serve\Cancellation;

/** Cancels a token from a SIGALRM while the test thread sits in a lock wait (needs pcntl). */
final class Alarm
{
    public static function available(): bool
    {
        return function_exists('pcntl_alarm');
    }

    /** @return Closure(): void stops the alarm; call it in `finally` */
    public static function cancelAfter(Cancellation $cancel, int $seconds): Closure
    {
        pcntl_async_signals(true);
        pcntl_signal(SIGALRM, static function () use ($cancel): void {
            $cancel->cancel();
        });
        pcntl_alarm($seconds);

        return static function (): void {
            pcntl_alarm(0);
            pcntl_signal(SIGALRM, SIG_DFL);
        };
    }
}
