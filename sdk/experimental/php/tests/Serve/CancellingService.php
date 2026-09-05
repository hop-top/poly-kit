<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Serve\Service;

/**
 * A service that trips the run's cancellation from inside its own
 * tick, standing in for a signal handler firing while the supervisor
 * is serving.
 *
 * This is how a test reaches the state a real SIGTERM produces —
 * cancelled *after* readiness, mid-serve — without delivering a signal
 * to the PHPUnit process itself.
 */
final class CancellingService implements Service
{
    public int $tickCount = 0;
    public int $stopCount = 0;

    private bool $started = false;

    public function __construct(
        private readonly string $name,
        private readonly Cancellation $cancel,
        private readonly int $afterTicks = 1,
    ) {
    }

    public function name(): string
    {
        return $this->name;
    }

    public function start(): void
    {
        $this->started = true;
    }

    public function ready(): bool
    {
        return $this->started;
    }

    public function tick(): bool
    {
        $this->tickCount++;
        if ($this->tickCount >= $this->afterTicks) {
            $this->cancel->cancel();
        }
        // Never finishes on its own: only the cancellation ends it, so
        // the test proves the drain is what stopped the run.
        return true;
    }

    public function stop(): void
    {
        $this->stopCount++;
    }
}
