<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Command;
use HopTop\Kit\Mcp\Dispatcher;
use HopTop\Kit\Mcp\LegacyHandler;
use HopTop\Kit\Mcp\ModernHandler;
use HopTop\Kit\Mcp\Mount;
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
 * The fixture carries three sections and all three are replayed:
 *
 *  - `cases` each get a **fresh** mount, so no case can observe state left
 *    behind by an earlier one.
 *  - `sequences` are the deliberate exception: ordered steps replayed
 *    against **one long-lived** mount, capturing behaviour only a
 *    persistent server exhibits — which is how adopters actually deploy.
 *  - `mrtr` is the one exchange that cannot be byte-exact throughout,
 *    because round 1 mints a fresh time-bound `requestState`. Its round 1
 *    is checked by shape, its round 2 byte-exactly against Go.
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
     * The MRTR confirmation loop: the third fixture section, and the only
     * one that is not byte-exact end to end.
     *
     * Round 1 mints a fresh, time-bound `requestState` whose MAC differs
     * every run, so only its SHAPE is assertable — the fixture names the
     * members that must be present and the ones that must never appear.
     * Round 2 echoes that state back into a template and IS byte-exact,
     * which is what makes the exchange verifiable rather than merely
     * plausible: a port that fabricated a plausible-looking round 1 could
     * not produce a state its own round 2 accepts *and* land on Go's exact
     * bytes.
     *
     * Both rounds run against ONE mount keyed with the fixture's
     * `confirmation_key`. The state is a MAC over that key, so a mount
     * carrying a different key cannot replay round 2.
     */
    #[Test]
    public function theMrtrLoopPromptsThenExecutesOnlyOnAcceptedRetry(): void
    {
        $mrtr = self::mrtrSection();

        [$tree, $executions] = LockTrees::mrtr();

        $dispatcher = (new Mount(confirmationKey: $mrtr['confirmation_key']))
            ->dispatcher(new Bridge($tree, Policy::default()));

        // --- Round 1: the prompt ---------------------------------------
        $first = $dispatcher->dispatch(
            $mrtr['round1_request'],
            self::headers(['headers' => $mrtr['round1_headers']]),
        );

        self::assertSame($mrtr['round1_status'], $first->status, 'round 1: HTTP status');

        $result = self::resultOf($first, 'round 1');

        // The leaf must NOT have run: that is the entire defect this
        // section exists to catch. A port gating on X-Confirm-Token alone
        // would have refused with 428 above; a port with no gate at all
        // would have executed here.
        self::assertSame(0, $executions->n, 'round 1: leaf executed before confirmation');

        foreach ($mrtr['round1_must_have'] as $path => $want) {
            self::assertSame($want, self::dig($result, $path), 'round 1: '.$path);
        }

        foreach ($mrtr['round1_must_not_have'] as $absent) {
            self::assertArrayNotHasKey($absent, $result, 'round 1 must not carry '.$absent);
        }

        // Exactly one entry, under the reserved "confirm" key.
        self::assertSame(
            ['confirm'],
            array_keys(self::childArray($result, 'inputRequests')),
            'round 1: inputRequests keys',
        );

        // `v1.<expiry-base10>.<mac>` — three dot-separated parts. The MAC
        // is production-derived and never compared.
        self::assertIsString($result['requestState'] ?? null, 'round 1: requestState is a string');
        $state = (string) $result['requestState'];
        $parts = explode('.', $state);

        self::assertCount(3, $parts, 'round 1: requestState part count');
        self::assertSame('v1', $parts[0], 'round 1: requestState version');
        self::assertMatchesRegularExpression('/^\d+$/', $parts[1], 'round 1: requestState expiry is base-10');
        self::assertNotSame('', $parts[2], 'round 1: requestState mac is non-empty');

        // --- Round 2: the accepted retry, byte-exact -------------------
        $second = $dispatcher->dispatch(
            str_replace('{{requestState}}', $state, $mrtr['round2_request_template']),
            self::headers(['headers' => $mrtr['round2_headers']]),
        );

        self::assertSame($mrtr['round2_status'], $second->status, 'round 2: HTTP status');
        self::assertSame(
            $mrtr['round2_response'],
            $second->body,
            'round 2: wire body must be byte-identical',
        );
        self::assertSame(1, $executions->n, 'round 2: leaf must run exactly once after accept');
    }

    /**
     * A state minted under a different key cannot replay round 2.
     *
     * The happy-path exchange above only ever presents a genuine state, so
     * on its own it cannot tell a mount that verifies the MAC from one that
     * waves any well-formed token through. This replays the fixture's own
     * round 2 against a mount keyed differently: the token is correctly
     * framed and correctly bound to the call, and must still be refused.
     */
    #[Test]
    public function theMrtrRetryIsRefusedWhenTheStateWasMintedUnderAnotherKey(): void
    {
        $mrtr = self::mrtrSection();

        // The forger's mount: same tree, same call, different secret.
        [$forgerTree] = LockTrees::mrtr();
        $forger = (new Mount(confirmationKey: 'a-different-suite-shared-secret!!'))
            ->dispatcher(new Bridge($forgerTree, Policy::default()));

        $minted = self::resultOf(
            $forger->dispatch(
                $mrtr['round1_request'],
                self::headers(['headers' => $mrtr['round1_headers']]),
            ),
            'foreign round 1',
        );

        self::assertIsString($minted['requestState'] ?? null, 'foreign round 1: requestState is a string');

        [$tree, $executions] = LockTrees::mrtr();
        $dispatcher = (new Mount(confirmationKey: $mrtr['confirmation_key']))
            ->dispatcher(new Bridge($tree, Policy::default()));

        $response = $dispatcher->dispatch(
            str_replace('{{requestState}}', (string) $minted['requestState'], $mrtr['round2_request_template']),
            self::headers(['headers' => $mrtr['round2_headers']]),
        );

        // Re-prompting rather than erroring is the deliberate choice: the
        // user can still approve, and a token error is not actionable by
        // them. What must never happen is the leaf running.
        $result = self::resultOf($response, 'foreign round 2');

        self::assertSame('input_required', $result['resultType'], 'foreign round 2: must re-prompt');
        self::assertSame(0, $executions->n, 'foreign round 2: leaf ran on an unverifiable state');
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
        /** @var list<array<string, mixed>> $section */
        $section = self::fixture()[$name] ?? self::fail('fixture is missing the '.$name.' section');

        return $section;
    }

    /**
     * Reads the `mrtr` section, which is a single object rather than a list.
     *
     * @return array{
     *     confirmation_key: string,
     *     round1_headers: array<string, string>,
     *     round1_request: string,
     *     round1_status: int,
     *     round1_must_have: array<string, string>,
     *     round1_must_not_have: list<string>,
     *     round2_headers: array<string, string>,
     *     round2_request_template: string,
     *     round2_status: int,
     *     round2_response: string,
     * }
     */
    private static function mrtrSection(): array
    {
        /** @var array{confirmation_key: string, round1_headers: array<string, string>, round1_request: string, round1_status: int, round1_must_have: array<string, string>, round1_must_not_have: list<string>, round2_headers: array<string, string>, round2_request_template: string, round2_status: int, round2_response: string} $mrtr */
        $mrtr = self::fixture()['mrtr'] ?? self::fail('fixture is missing the mrtr section');

        return $mrtr;
    }

    /**
     * Decodes the shared fixture document.
     *
     * @return array<string, mixed>
     */
    private static function fixture(): array
    {
        $raw = file_get_contents(self::FIXTURE);
        self::assertIsString($raw, 'wire fixtures must be readable');

        /** @var array<string, mixed> $decoded */
        $decoded = json_decode($raw, true, 512, \JSON_THROW_ON_ERROR);

        return $decoded;
    }

    /**
     * Decodes a response and returns its `result` member.
     *
     * @return array<string, mixed>
     */
    private static function resultOf(Response $response, string $label): array
    {
        /** @var array<string, mixed> $decoded */
        $decoded = json_decode($response->body, true, 512, \JSON_THROW_ON_ERROR);

        self::assertArrayHasKey('result', $decoded, $label.': response carries a result');
        self::assertIsArray($decoded['result'], $label.': result is an object');

        /** @var array<string, mixed> $result */
        $result = $decoded['result'];

        return $result;
    }

    /**
     * Reads a nested object member, failing rather than returning null.
     *
     * @param array<string, mixed> $result
     *
     * @return array<string, mixed>
     */
    private static function childArray(array $result, string $key): array
    {
        self::assertIsArray($result[$key] ?? null, $key.' must be an object');

        /** @var array<string, mixed> $child */
        $child = $result[$key];

        return $child;
    }

    /**
     * Reads a dotted path out of a decoded result, for the fixture's
     * shape assertions.
     *
     * @param array<string, mixed> $root
     */
    private static function dig(array $root, string $path): mixed
    {
        $cursor = $root;

        foreach (explode('.', $path) as $segment) {
            if (!\is_array($cursor) || !\array_key_exists($segment, $cursor)) {
                return null;
            }

            /** @var mixed $cursor */
            $cursor = $cursor[$segment];
        }

        return $cursor;
    }
}
