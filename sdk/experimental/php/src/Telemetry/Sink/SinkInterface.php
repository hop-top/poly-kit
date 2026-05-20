<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry\Sink;

/**
 * Common surface for telemetry sinks.
 *
 * Sinks are publish-only: a sink enqueues envelopes and flushes them to
 * its underlying transport (filesystem, HTTPS, ...). Sinks MUST be
 * non-fatal — every operation is best-effort and never throws.
 *
 * Stats keys (canonical):
 *   - emitted (int): envelopes successfully written to the transport.
 *   - dropped (int): envelopes refused (queue full) or failed terminally.
 *   - queued  (int): envelopes currently buffered, awaiting flush.
 *
 * Implementations MAY add transport-specific keys (e.g. `path` for the
 * JSONL sink) and consumers MUST NOT depend on the absence of extras.
 */
interface SinkInterface
{
    /**
     * Buffer an envelope for later transport. Best-effort: a sink whose
     * queue is full MUST increment the dropped counter and return — never
     * block, never throw.
     *
     * @param array<string, mixed> $envelope
     */
    public function enqueue(array $envelope): void;

    /**
     * Drain the queue to the underlying transport. Best-effort: I/O or
     * network failures MUST be swallowed (and accounted for in stats).
     */
    public function flush(): void;

    /**
     * Snapshot of the sink's counters. See class doc for canonical keys.
     *
     * @return array<string, mixed>
     */
    public function stats(): array;
}
