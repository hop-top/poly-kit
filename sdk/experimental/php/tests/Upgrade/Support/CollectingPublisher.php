<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

use HopTop\Kit\Upgrade\EventPublisher;

/** Collects events as `{topic, payload}`, with `duration_ms` checked and removed. */
final class CollectingPublisher implements EventPublisher
{
    /** @var list<array{topic: string, payload: array<string, mixed>}> */
    public array $events = [];

    /** @var list<string> */
    public array $problems = [];

    public function publish(string $topic, string $source, array $payload): void
    {
        if ($source !== 'core.upgrade') {
            $this->problems[] = "source = $source";
        }
        if (array_key_exists('duration_ms', $payload)) {
            $d = $payload['duration_ms'];
            if (!is_int($d) || $d < 0) {
                $this->problems[] = "$topic: duration_ms = " . var_export($d, true);
            }
            unset($payload['duration_ms']);
        }
        $this->events[] = ['topic' => $topic, 'payload' => $payload];
    }
}
