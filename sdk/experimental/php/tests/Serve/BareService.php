<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Serve\Service;

/**
 * A service carrying only the four required capabilities and none of
 * the optional declarations — the shape the contract calls the
 * minimum. Used where a test must prove the supervisor asks for a
 * declaration rather than assuming one.
 */
final class BareService implements Service
{
    public int $stopCount = 0;

    private bool $started = false;
    private int $ticks = 0;

    public function __construct(
        private readonly string $name,
        private readonly ?int $finishAfterTicks = 1,
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
        $this->ticks++;
        return $this->finishAfterTicks === null || $this->ticks < $this->finishAfterTicks;
    }

    public function stop(): void
    {
        $this->stopCount++;
    }
}
