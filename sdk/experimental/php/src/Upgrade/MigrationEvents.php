<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use Exception;

/**
 * Publishes migration events to an optional `EventPublisher`; without one it
 * is silent.
 *
 * @internal
 */
final class MigrationEvents
{
    private const SOURCE = 'core.upgrade';

    public function __construct(private readonly ?EventPublisher $publisher) {}

    /** @param array<string, mixed> $payload */
    public function emit(string $topic, array $payload): void
    {
        if ($this->publisher === null || $topic === '') {
            return;
        }
        try {
            $this->publisher->publish($topic, self::SOURCE, $payload);
        } catch (Exception) {
            // publisher errors never fail a run
        }
    }

    /** Whole milliseconds since an `hrtime(true)` reading. */
    public static function millis(int $since): int
    {
        return intdiv(hrtime(true) - $since, 1_000_000);
    }
}
