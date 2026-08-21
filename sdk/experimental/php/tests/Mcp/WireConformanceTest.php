<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Command;
use HopTop\Kit\Mcp\Dispatcher;
use HopTop\Kit\Mcp\LegacyHandler;
use HopTop\Kit\Mcp\ModernHandler;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\Response;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * Replays the shared cross-language wire fixtures against this port.
 *
 * The fixtures are the parity contract: they were generated from the Go
 * surface, and where they and the design documents disagree the fixtures
 * win. Comparison is on raw bytes with no decode/re-encode step, which is
 * the only way key order, escaping and the trailing newline are actually
 * checked.
 *
 * Each case gets a fresh mount, so no case can observe state left behind
 * by an earlier one and every case is independently reproducible.
 */
final class WireConformanceTest extends TestCase
{
    private const FIXTURE = __DIR__.'/../../../../tests/cross-lang/fixtures/mcp-wire.json';

    #[Test]
    public function everyFixtureCaseMatchesByteForByte(): void
    {
        $cases = self::cases();

        self::assertCount(18, $cases, 'fixture count changed — the parity contract moved');

        foreach ($cases as $case) {
            $tree = str_starts_with($case['name'], 'legacy/')
                ? LockTrees::legacy()
                : LockTrees::modern();

            $response = self::mount($tree)->dispatch($case['request'], self::headers($case));

            self::assertSame(
                $case['status'],
                $response->status,
                \sprintf('%s: HTTP status', $case['name']),
            );

            self::assertSame(
                $case['response'],
                $response->body,
                \sprintf('%s: wire body must be byte-identical', $case['name']),
            );
        }
    }

    /**
     * Builds a default mount: both eras, the conservative policy, and the
     * fixtures' server identity and cache hints.
     */
    private static function mount(Command $root): Dispatcher
    {
        $bridge = new Bridge($root, Policy::default());

        return new Dispatcher(
            legacy: new LegacyHandler($bridge),
            modern: new ModernHandler($bridge),
        );
    }

    /**
     * @param array<string, mixed> $case
     *
     * @return array<string, list<string>>
     */
    private static function headers(array $case): array
    {
        $headers = [];

        foreach ($case['headers'] ?? [] as $name => $value) {
            $headers[$name] = [$value];
        }

        return $headers;
    }

    /** @return list<array<string, mixed>> */
    private static function cases(): array
    {
        $raw = file_get_contents(self::FIXTURE);
        self::assertIsString($raw, 'wire fixtures must be readable');

        /** @var array{cases: list<array<string, mixed>>} $decoded */
        $decoded = json_decode($raw, true, 512, \JSON_THROW_ON_ERROR);

        return $decoded['cases'];
    }
}
