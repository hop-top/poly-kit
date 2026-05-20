<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry\Sink;

/**
 * Drops every envelope. Selected by `KIT_TELEMETRY_SINK=none`.
 *
 * Useful for adopters who want the facade plumbed through their code
 * (so call sites compile) but explicitly no I/O — e.g. in CI, in tests
 * that exercise telemetry-adjacent code without exporting, or while
 * staging an HTTPS rollout.
 */
final class NullSink implements SinkInterface
{
    private int $dropped = 0;

    public function enqueue(array $envelope): void
    {
        $this->dropped++;
    }

    public function flush(): void
    {
        // Nothing to do.
    }

    public function stats(): array
    {
        return [
            'emitted' => 0,
            'dropped' => $this->dropped,
            'queued' => 0,
        ];
    }
}
