<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

use Throwable;

/**
 * The mutable half of one run, kept off the Supervisor so two runs in
 * one process never share state.
 *
 * @internal
 */
final class RunState
{
    /** @var list<string> */
    public array $started = [];

    /** @var list<string> */
    public array $ready = [];

    /** @var array<string, string> */
    public array $failed = [];

    /** @var array<string, Throwable> */
    public array $sources = [];

    /** @var list<Outcome> */
    public array $observed = [];

    /** @var array<string, true> */
    private array $stopped = [];

    private readonly float $begin;

    /** @param callable():float $now */
    public function __construct(private $now)
    {
        $this->begin = ($now)();
    }

    public function elapsedMs(): int
    {
        return (int) round((($this->now)() - $this->begin) * 1000);
    }

    /**
     * Records that $name's stopped event has been surfaced and reports
     * whether it had been already. A service reports stopped once per
     * run, whichever path noticed it first.
     */
    public function markStopped(string $name): bool
    {
        $already = isset($this->stopped[$name]);
        $this->stopped[$name] = true;
        return $already;
    }
}
