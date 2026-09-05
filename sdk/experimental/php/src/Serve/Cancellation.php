<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * A one-way latch the supervisor cancels once so every service
 * observes cancellation at the same instant.
 *
 * The Go port uses a context and the TS port an AbortSignal; PHP has
 * neither, and this is the smallest thing that carries the property
 * the contract actually names — "cancel once, so nothing is queued
 * behind another service's drain".
 */
final class Cancellation
{
    private bool $cancelled = false;

    /** @var list<callable():void> */
    private array $listeners = [];

    /** Whether cancellation has been requested. */
    public function cancelled(): bool
    {
        return $this->cancelled;
    }

    /** Cancels once. Later calls are no-ops, so a cancel is idempotent. */
    public function cancel(): void
    {
        if ($this->cancelled) {
            return;
        }
        $this->cancelled = true;
        foreach ($this->listeners as $listener) {
            $listener();
        }
        $this->listeners = [];
    }

    /**
     * Runs $listener on cancellation, or immediately when already
     * cancelled — so a late subscriber cannot miss the edge.
     *
     * @param callable():void $listener
     */
    public function onCancel(callable $listener): void
    {
        if ($this->cancelled) {
            $listener();
            return;
        }
        $this->listeners[] = $listener;
    }
}
