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
 * The fixture carries two sections and both are replayed:
 *
 *  - `cases` each get a **fresh** mount, so no case can observe state left
 *    behind by an earlier one.
 *  - `sequences` are the deliberate exception: ordered steps replayed
 *    against **one long-lived** mount, capturing behaviour only a
 *    persistent server exhibits — which is how adopters actually deploy.
 */
final class WireConformanceTest extends TestCase
{
    private const FIXTURE = __DIR__.'/../../../../tests/cross-lang/fixtures/mcp-wire.json';

    #[Test]
    public function everyFixtureCaseMatchesByteForByte(): void
    {
        $cases = self::section('cases');

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

    #[Test]
    public function everySequenceReplaysAgainstOneLongLivedMount(): void
    {
        $sequences = self::section('sequences');

        self::assertNotEmpty($sequences, 'the long-lived-mount contract must be exercised');

        foreach ($sequences as $sequence) {
            $tree = str_starts_with($sequence['name'], 'legacy/')
                ? LockTrees::legacy()
                : LockTrees::modern();

            // One mount for the whole sequence: the steps are only
            // meaningful in order, against accumulated state.
            $dispatcher = self::mount($tree);

            foreach ($sequence['steps'] as $step) {
                $response = $dispatcher->dispatch($step['request'], self::headers($step));

                $label = $sequence['name'].'/'.$step['name'];

                self::assertSame($step['status'], $response->status, $label.': HTTP status');
                self::assertSame(
                    $step['response'],
                    $response->body,
                    $label.': wire body must be byte-identical',
                );
            }
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
     * Normalises a case's or step's headers to PSR-style multi-value lists.
     *
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

    /**
     * Reads one top-level array from the shared fixture.
     *
     * @return list<array<string, mixed>>
     */
    private static function section(string $name): array
    {
        $raw = file_get_contents(self::FIXTURE);
        self::assertIsString($raw, 'wire fixtures must be readable');

        /** @var array<string, list<array<string, mixed>>> $decoded */
        $decoded = json_decode($raw, true, 512, \JSON_THROW_ON_ERROR);

        self::assertArrayHasKey($name, $decoded, 'fixture is missing the '.$name.' section');

        return $decoded[$name];
    }
}
