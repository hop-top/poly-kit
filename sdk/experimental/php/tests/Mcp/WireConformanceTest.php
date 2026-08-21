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
 * Cases are replayed **in order against one mount per era**, because the
 * fixtures capture stateful behaviour: invoking a leaf registers cobra's
 * implicit `--help` flag, so a `tools/list` taken after a `tools/call`
 * legitimately differs from one taken before. Replaying each case against
 * a fresh mount would hide that and quietly break parity.
 */
final class WireConformanceTest extends TestCase
{
    private const FIXTURE = __DIR__.'/../../../../tests/cross-lang/fixtures/mcp-wire.json';

    #[Test]
    public function everyFixtureCaseMatchesByteForByte(): void
    {
        $cases = self::cases();

        self::assertCount(17, $cases, 'fixture count changed — the parity contract moved');

        $mounts = [
            'legacy' => self::mount(LockTrees::legacy()),
            'modern' => self::mount(LockTrees::modern()),
        ];

        foreach ($cases as $case) {
            $dispatcher = $mounts[str_starts_with($case['name'], 'legacy/') ? 'legacy' : 'modern'];

            $response = $dispatcher->dispatch($case['request'], self::headers($case));

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

    #[Test]
    public function legacyAndModernListsDivergeOnlyAfterAnInvocation(): void
    {
        // Guards the stateful behaviour the replay above depends on: the
        // implicit help flag must appear only once its leaf has run.
        $dispatcher = self::mount(LockTrees::legacy());

        $before = $dispatcher->dispatch('{"jsonrpc":"2.0","id":1,"method":"tools/list"}', []);
        self::assertStringNotContainsString('help for ping', $before->body);

        $dispatcher->dispatch(
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping"}}',
            [],
        );

        $after = $dispatcher->dispatch('{"jsonrpc":"2.0","id":3,"method":"tools/list"}', []);
        self::assertStringContainsString('help for ping', $after->body);
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
