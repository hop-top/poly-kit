<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Serve\ServeLogger;

/**
 * Captures the lifecycle records so a test can assert the transitions
 * were surfaced with the contract's vocabulary and field names.
 */
final class RecordingLogger implements ServeLogger
{
    /** @var list<array{level: string, message: string, fields: array<string, scalar|null>}> */
    public array $records = [];

    public function info(string $message, array $fields = []): void
    {
        $this->records[] = ['level' => 'info', 'message' => $message, 'fields' => $fields];
    }

    public function error(string $message, array $fields = []): void
    {
        $this->records[] = ['level' => 'error', 'message' => $message, 'fields' => $fields];
    }

    /**
     * Every record published under $topic.
     *
     * @return list<array{level: string, message: string, fields: array<string, scalar|null>}>
     */
    public function withTopic(string $topic): array
    {
        return array_values(array_filter(
            $this->records,
            static fn (array $r): bool => ($r['fields']['topic'] ?? null) === $topic,
        ));
    }

    /** @return list<string> Every topic surfaced, in order. */
    public function topics(): array
    {
        $out = [];
        foreach ($this->records as $r) {
            $topic = $r['fields']['topic'] ?? null;
            if (is_string($topic) && $topic !== '') {
                $out[] = $topic;
            }
        }
        return $out;
    }
}
