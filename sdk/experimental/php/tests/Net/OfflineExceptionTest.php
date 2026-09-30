<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Net;

use HopTop\Kit\Net\OfflineException;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Assert;
use PHPUnit\Framework\TestCase;

/**
 * The refusal names where the request was going, never what it carried:
 * scheme, host (with port) and path only. Query, fragment and userinfo
 * may hold credentials (an API key param, basic-auth userinfo), so the
 * factory drops them whoever calls it — the guards, or an adopter
 * building the exception by hand.
 */
class OfflineExceptionTest extends TestCase
{
    public const SECRET_URL = 'https://alice:pw-secret@example.invalid/v1/models/m:generate'
        . '?alt=sse&key=q-secret#frag-secret';

    private const LEAKS = ['alice', 'pw-secret', 'q-secret', 'key=', 'alt=sse', 'frag-secret', '?', '#', '@'];

    public static function assertNoLeak(string $message): void
    {
        foreach (self::LEAKS as $leak) {
            Assert::assertStringNotContainsString($leak, $message, "refusal carries {$leak}");
        }
    }

    /**
     * @return iterable<string, array{string, string}>
     */
    public static function destinations(): iterable
    {
        yield 'userinfo, query, fragment' => [
            self::SECRET_URL,
            'GET https://example.invalid/v1/models/m:generate: network disabled by --offline',
        ];
        yield 'port kept' => [
            'http://u:p@example.invalid:8443/x?key=q-secret',
            'GET http://example.invalid:8443/x: network disabled by --offline',
        ];
        yield 'ipv6 literal' => [
            'http://[2001:db8::1]:9000/x?key=q-secret',
            'GET http://[2001:db8::1]:9000/x: network disabled by --offline',
        ];
        yield 'opaque keeps scheme alone' => [
            'mailto:alice@example.invalid?subject=q-secret',
            'GET mailto:: network disabled by --offline',
        ];
        yield 'unparseable echoes nothing' => [
            'http:///alice:pw-secret@?key=q-secret',
            'GET <unparseable URL>: network disabled by --offline',
        ];
    }

    #[DataProvider('destinations')]
    public function testForRequestKeepsDestinationOnly(string $url, string $want): void
    {
        $e = OfflineException::forRequest('GET', $url);

        $this->assertSame($want, $e->getMessage());
        self::assertNoLeak($e->getMessage());
    }
}
