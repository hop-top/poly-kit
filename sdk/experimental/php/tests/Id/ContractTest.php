<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Id;

use HopTop\Kit\Id\Id;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

/**
 * Cross-language parity contract test (tlc T-0753).
 *
 * Loads `contracts/typeid-v1/fixtures.json` from the repo root and
 * asserts that this PHP SDK's encode (Id::fromUuid) and decode
 * (Id::parse) agree with the canonical wire form shared by
 * go/rs/ts/py. A divergence here means either the upstream
 * `jewei/typeid-php` package drifted or the contract was edited
 * without updating all five SDKs.
 */
final class ContractTest extends TestCase
{
    /**
     * Walk up from this file's directory until we hit the kit repo
     * root that contains contracts/typeid-v1/fixtures.json. Tied to
     * __DIR__ rather than CWD so the loader works under both
     * `vendor/bin/phpunit` (CWD = sdk/experimental/php) and editor
     * invocations whose CWD may vary.
     */
    private static function locateContract(): string
    {
        $dir = __DIR__;
        for ($i = 0; $i < 10; $i++) {
            $candidate = $dir . '/contracts/typeid-v1/fixtures.json';
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
            'contracts/typeid-v1/fixtures.json: not found walking up from '.__DIR__,
        );
    }

    /**
     * @return array{version: string, spec: string, vectors: list<array<string, mixed>>}
     */
    private static function loadContract(): array
    {
        $raw = file_get_contents(self::locateContract());
        if ($raw === false) {
            throw new \RuntimeException('failed to read contracts/typeid-v1/fixtures.json');
        }
        $data = json_decode($raw, true, flags: JSON_THROW_ON_ERROR);
        if (!is_array($data)) {
            throw new \RuntimeException('contracts/typeid-v1/fixtures.json: not a JSON object');
        }
        /** @var array{version: string, spec: string, vectors: list<array<string, mixed>>} $data */
        return $data;
    }

    /**
     * @return iterable<string, array{0: array<string, mixed>}>
     */
    public static function vectors(): iterable
    {
        foreach (self::loadContract()['vectors'] as $v) {
            /** @var array<string, mixed> $v */
            yield (string) $v['name'] => [$v];
        }
    }

    public function testContractMetadata(): void
    {
        $cf = self::loadContract();
        $this->assertSame('v1', $cf['version'], 'contract version drift');
        $this->assertSame('jetify-typeid-v0.3', $cf['spec'], 'contract spec drift');
        $this->assertNotEmpty($cf['vectors'], 'contract has no vectors');
    }

    /**
     * @param array<string, mixed> $v
     */
    #[DataProvider('vectors')]
    public function testContractGeneration(array $v): void
    {
        /** @var list<string> $skipIn */
        $skipIn = $v['skip_in'] ?? [];
        if (in_array('php', $skipIn, true)) {
            $note = (string) ($v['note'] ?? '');
            $this->markTestSkipped("skip_in includes 'php': {$note}");
        }

        $got = Id::fromUuid((string) $v['prefix'], (string) $v['uuid']);
        $this->assertSame(
            (string) $v['typeid'],
            $got,
            sprintf(
                "canonical typeid drift on vector %s (prefix=%s uuid=%s)",
                (string) $v['name'],
                (string) $v['prefix'],
                (string) $v['uuid'],
            ),
        );
    }

    /**
     * @param array<string, mixed> $v
     */
    #[DataProvider('vectors')]
    public function testContractParse(array $v): void
    {
        /** @var list<string> $skipIn */
        $skipIn = $v['skip_in'] ?? [];
        if (in_array('php', $skipIn, true)) {
            $note = (string) ($v['note'] ?? '');
            $this->markTestSkipped("skip_in includes 'php': {$note}");
        }

        $parsed = Id::parse((string) $v['typeid']);
        $this->assertSame(
            (string) $v['prefix'],
            $parsed->prefix,
            "prefix mismatch on vector ".(string) $v['name'],
        );
        $this->assertSame(
            (string) $v['uuid'],
            $parsed->uuid,
            "uuid mismatch on vector ".(string) $v['name'],
        );
    }
}
