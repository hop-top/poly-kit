<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade;

use HopTop\Kit\Upgrade\DocApplyException;
use HopTop\Kit\Upgrade\DocInvalidException;
use HopTop\Kit\Upgrade\DocOptions;
use HopTop\Kit\Upgrade\DocStep;
use HopTop\Kit\Upgrade\DocumentMigrator;
use HopTop\Kit\Upgrade\Migration;
use HopTop\Kit\Upgrade\Registry;
use HopTop\Kit\Upgrade\Semver;
use HopTop\Kit\Upgrade\UpgradeException;
use HopTop\Kit\Upgrade\VerifyException;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

final class UpgradeTest extends TestCase
{
    #[Test]
    public function semverParsesLeniently(): void
    {
        $v = Semver::parse('v1.2.3.4-rc.1+build');
        self::assertSame([1, 2, 3, 'rc.1'], [$v->major, $v->minor, $v->patch, $v->pre]);
        $v = Semver::parse('x.y');
        self::assertSame([0, 0, 0, ''], [$v->major, $v->minor, $v->patch, $v->pre]);
        self::assertSame(0, Semver::compare('', '0.0.0'));
    }

    #[Test]
    public function semverDiffersFromVersionCompare(): void
    {
        // version_compare orders "1.0.0-alpha" and "1.0.0-alpha.1" differently
        // from the contract and treats 'v' prefixes and build metadata as text.
        self::assertSame(-1, Semver::compare('1.0.0-alpha', '1.0.0-alpha.1'));
        self::assertSame(0, Semver::compare('1.0.0+a', '1.0.0+b'));
        self::assertSame(-1, Semver::compare('1.0.0-2', '1.0.0-10'));
        self::assertSame(-1, Semver::compare('1.0.0-9', '1.0.0-alpha'));
    }

    #[Test]
    public function registryDefaultIsSharedAndOwnIsIsolated(): void
    {
        $noop = static function (): void {};
        Registry::default()->reset();
        self::assertSame(Registry::default(), Registry::default());
        Registry::default()->register(new Migration('1.1.0', 's', $noop));
        $own = new Registry();
        self::assertSame([], $own->pending('s', '1.0.0', '2.0.0'));
        self::assertCount(1, Registry::default()->pending('s', '1.0.0', '2.0.0'));
        self::assertSame([], Registry::default()->pending('other', '1.0.0', '2.0.0'));
        Registry::default()->reset();
        self::assertSame([], Registry::default()->pending('s', '1.0.0', '2.0.0'));
    }

    #[Test]
    public function registryOrdersAscendingAndKeepsRegisteredVersions(): void
    {
        $noop = static function (): void {};
        $r = new Registry();
        foreach (['v1.10.0', '1.2.0', '1.9.0'] as $v) {
            $r->register(new Migration($v, 's', $noop));
        }
        $got = array_map(static fn (Migration $m): string => $m->version, $r->pending('s', '', 'v2'));
        self::assertSame(['1.2.0', '1.9.0', 'v1.10.0'], $got);
    }

    private static function step(int $from, int $to, ?\Closure $apply = null, ?\Closure $verify = null): DocStep
    {
        return new DocStep($from, $to, $apply ?? static fn (array $d): array => $d, $verify, "n$from");
    }

    #[Test]
    public function emptyObjectsSurviveAndKeysAreSorted(): void
    {
        $r = DocumentMigrator::migrate('{"version":1,"z":{},"a":[],"m":{"y":1,"x":2}}', [self::step(1, 2)]);
        self::assertSame('{"a":[],"m":{"x":2,"y":1},"version":2,"z":{}}', $r->document);
    }

    #[Test]
    public function currentDocumentComesBackAsTheInputText(): void
    {
        $in = "{ \"version\" : 1,\n \"z\":1 }";
        $r = DocumentMigrator::migrate($in, [self::step(0, 1)]);
        self::assertSame($in, $r->document);
        self::assertSame([], $r->report->applied);
    }

    #[Test]
    public function badTextAndBadStepsAreInvalid(): void
    {
        $this->expectException(DocInvalidException::class);
        DocumentMigrator::migrate('{nope', [self::step(1, 2)]);
    }

    #[Test]
    public function aStepReturningNullFailsAndKeepsTheCause(): void
    {
        try {
            DocumentMigrator::migrate('{"version":1}', [self::step(1, 2, static fn (array $d): ?array => null)]);
            self::fail('no error');
        } catch (DocApplyException $e) {
            self::assertSame('migration 1 -> 2 returned nil map', $e->getMessage());
        }
        $boom = new \LogicException('boom');
        try {
            DocumentMigrator::migrate('{"version":1}', [self::step(1, 2, static fn (array $d): array => throw $boom)]);
            self::fail('no error');
        } catch (DocApplyException $e) {
            self::assertSame('migration 1 -> 2: boom', $e->getMessage());
            self::assertSame($boom, $e->getPrevious());
        }
    }

    #[Test]
    public function verifyFailureIsAVerifyException(): void
    {
        $this->expectException(VerifyException::class);
        $this->expectExceptionMessage('migration 1 -> 2: verify: nope');
        DocumentMigrator::migrate('{"version":1}', [self::step(1, 2, null, static fn (array $d) => throw new \RuntimeException('nope'))]);
    }

    #[Test]
    public function everyFailureIsAnUpgradeException(): void
    {
        $this->expectException(UpgradeException::class);
        DocumentMigrator::migrate('{"version":9}', [self::step(1, 2)]);
    }

    #[Test]
    public function migrateArrayLeavesTheCallersArrayAlone(): void
    {
        $in = ['version' => 1, 'k' => ['a' => 1]];
        $r = DocumentMigrator::migrateArray($in, [self::step(1, 2, static function (array $d): array {
            $d['k']['a'] = 2;

            return $d;
        })], new DocOptions());
        self::assertSame(['version' => 1, 'k' => ['a' => 1]], $in);
        self::assertSame(['version' => 2, 'k' => ['a' => 2]], $r->document);
    }
}
