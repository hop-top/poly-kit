<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * Receives migration lifecycle events. This SDK has no event bus, so the
 * migrator takes this one-method seam; an adapter over any dispatcher is a
 * few lines. Publishing is synchronous and in order; an exception thrown
 * here is dropped and never fails a run.
 */
interface EventPublisher
{
    /**
     * @param string $topic e.g. `kit.core.migration.applied`
     * @param string $source always `core.upgrade`
     * @param array<string, mixed> $payload `duration_ms` is an int when present
     */
    public function publish(string $topic, string $source, array $payload): void;
}
