<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Output;

use HopTop\Kit\Output\CliError;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

/**
 * Cross-language exit-taxonomy contract loader.
 *
 * Loads `contracts/exit-taxonomy-v1/taxonomy.json` — generated from
 * `go/console/output/envelope`, the single source of truth — and
 * asserts this PHP SDK's table agrees with it on every class, every
 * exit number and every transience.
 *
 * A failure here means Go's taxonomy moved and this port did not
 * follow. That is the exact drift this contract exists to catch: when
 * Go gained CONSENT_REFUSED 7 and PREREQUISITE 70, all four SDK ports
 * silently kept the old nine-class table for two releases, and
 * transienceForCode() returned "unknown" where Go returned
 * "transient" — so four runtimes handed agents wrong retry guidance.
 *
 * Fix by adding the missing class to the port, not by editing the
 * contract: the contract is regenerated from Go, never hand-edited.
 */
final class TaxonomyContractTest extends TestCase
{
    /**
     * The slots envelope itself owns. The conformance-tree slots
     * (66-69) are recorded in the contract so a new allocation cannot
     * double-book a number, but no SDK port is expected to export
     * them.
     */
    private const string ENVELOPE_OWNER = 'hop.top/kit/go/console/output/envelope';

    /**
     * Walk up from this file's directory until we hit the kit repo
     * root that contains the taxonomy contract. Tied to __DIR__ rather
     * than CWD so the loader works under both `vendor/bin/phpunit`
     * (CWD = sdk/experimental/php) and editor invocations whose CWD
     * may vary.
     */
    private static function locateContract(): string
    {
        $dir = __DIR__;
        for ($i = 0; $i < 10; $i++) {
            $candidate = $dir . '/contracts/exit-taxonomy-v1/taxonomy.json';
            if (is_file($candidate)) {
                return $candidate;
            }
            $parent = dirname($dir);
            if ($parent === $dir) {
                break;
            }
            $dir = $parent;
        }
        throw new \RuntimeException(
            'contracts/exit-taxonomy-v1/taxonomy.json: not found walking up from '.__DIR__,
        );
    }

    /**
     * @return array<string, mixed>
     */
    private static function loadContract(): array
    {
        $raw = file_get_contents(self::locateContract());
        if ($raw === false) {
            throw new \RuntimeException('failed to read contracts/exit-taxonomy-v1/taxonomy.json');
        }
        $data = json_decode($raw, true, flags: JSON_THROW_ON_ERROR);
        if (!is_array($data)) {
            throw new \RuntimeException('contracts/exit-taxonomy-v1/taxonomy.json: not a JSON object');
        }

        /** @var array<string, mixed> $data */
        return $data;
    }

    /**
     * @return list<array<string, mixed>>
     */
    private static function classRows(): array
    {
        /** @var list<array<string, mixed>> $rows */
        $rows = self::loadContract()['classes'];

        return $rows;
    }

    /**
     * @return list<array<string, mixed>>
     */
    private static function bandRows(): array
    {
        /** @var list<array<string, mixed>> $rows */
        $rows = self::loadContract()['extension_band'];

        return $rows;
    }

    /**
     * @return iterable<string, array{0: array<string, mixed>}>
     */
    public static function classes(): iterable
    {
        foreach (self::classRows() as $row) {
            /** @var string $name */
            $name = $row['class'];
            yield $name => [$row];
        }
    }

    public function testContractMetadata(): void
    {
        $c = self::loadContract();
        self::assertSame('v1', $c['version']);
        self::assertSame(self::ENVELOPE_OWNER, $c['source']);
        self::assertNotEmpty($c['classes']);
    }

    /**
     * Membership in both directions. A port carrying an extra class Go
     * does not define is as much a divergence as a port missing one:
     * an adopter branching on it gets a number no other runtime
     * produces.
     */
    public function testDeclaresExactlyTheContractClasses(): void
    {
        $want = array_map(
            static fn (array $row): string => (string) $row['class'],
            self::classRows(),
        );
        self::assertSame($want, CliError::exitClasses());
    }

    /**
     * @param array<string, mixed> $row
     */
    #[DataProvider('classes')]
    public function testClassResolvesToContractExitAndTransience(array $row): void
    {
        $class = (string) $row['class'];
        self::assertSame($row['exit'], CliError::exitCodeForClass($class));
        self::assertSame($row['transience'], CliError::transienceForCode($class));
    }

    /**
     * The transience vocabulary is itself a contract: an agent
     * branching on a fourth string has no defined behavior.
     *
     * @param array<string, mixed> $row
     */
    #[DataProvider('classes')]
    public function testTransienceVocabularyIsTheContractVocabulary(array $row): void
    {
        /** @var list<string> $vocab */
        $vocab = self::loadContract()['transiences'];
        self::assertContains(CliError::transienceForCode((string) $row['class']), $vocab);
    }

    /**
     * An unknown class resolves to nothing, not to a fallback number.
     * A built-in fallback is what turns "kit added a class and this
     * port never copied it" into an assertion against exit 1 that
     * reads as a real failure rather than a stale table.
     */
    public function testDoesNotInventACodeForAnAdopterClass(): void
    {
        self::assertNull(CliError::exitCodeForClass('ADOPTER_SPECIFIC'));
        self::assertSame('unknown', CliError::transienceForCode('ADOPTER_SPECIFIC'));
    }

    public function testExportsEveryBandSlotEnvelopeOwns(): void
    {
        $owned = array_values(array_filter(
            self::bandRows(),
            static fn (array $s): bool => $s['owner'] === self::ENVELOPE_OWNER,
        ));
        self::assertNotEmpty($owned);
        foreach ($owned as $slot) {
            self::assertSame(
                $slot['exit'],
                CliError::exitCodeForClass((string) $slot['class']),
                sprintf('band slot %s must resolve to its contract exit', (string) $slot['class']),
            );
        }
    }

    /**
     * A port exporting LEAK_DETECTED would mint a number the
     * conformance tree owns, which is the collision the band record
     * exists to stop.
     */
    public function testLeavesForeignBandSlotsUnclaimed(): void
    {
        $foreign = array_values(array_filter(
            self::bandRows(),
            static fn (array $s): bool => $s['owner'] !== self::ENVELOPE_OWNER,
        ));
        self::assertNotEmpty($foreign);
        foreach ($foreign as $slot) {
            self::assertNull(
                CliError::exitCodeForClass((string) $slot['class']),
                sprintf(
                    'band slot %s is owned by %s, not this port',
                    (string) $slot['class'],
                    (string) $slot['owner'],
                ),
            );
        }
    }
}
