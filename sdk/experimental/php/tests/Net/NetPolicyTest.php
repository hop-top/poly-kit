<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Net;

use HopTop\Kit\Net\NetPolicy;
use PHPUnit\Framework\TestCase;

/**
 * Marker semantics. Mirrors the Go netpolicy context-tag tests: the
 * marker defaults off, flips on, and restores cleanly.
 */
class NetPolicyTest extends TestCase
{
    protected function setUp(): void
    {
        NetPolicy::reset();
    }

    protected function tearDown(): void
    {
        NetPolicy::reset();
    }

    public function testDefaultsToOnline(): void
    {
        $this->assertFalse(NetPolicy::isOffline());
    }

    public function testSetOfflineFlipsMarker(): void
    {
        NetPolicy::setOffline(true);
        $this->assertTrue(NetPolicy::isOffline());
    }

    public function testSetOfflineFalseLeavesMarkerClean(): void
    {
        NetPolicy::setOffline(false);
        $this->assertFalse(NetPolicy::isOffline());
    }

    public function testResetClearsMarker(): void
    {
        NetPolicy::setOffline(true);
        NetPolicy::reset();
        $this->assertFalse(NetPolicy::isOffline());
    }

    /**
     * The Go guard exempts loopback so `--offline` stays usable against a
     * local dev backend. Hosts that are not literal loopback IPs — DNS
     * names included — are remote, because resolving them is itself
     * network access.
     *
     * @dataProvider loopbackCases
     */
    public function testIsLoopbackHost(string $host, bool $want): void
    {
        $this->assertSame($want, NetPolicy::isLoopbackHost($host));
    }

    /** @return array<string, array{string, bool}> */
    public static function loopbackCases(): array
    {
        return [
            'ipv4 loopback' => ['127.0.0.1', true],
            'ipv4 loopback with port' => ['127.0.0.1:8080', true],
            'ipv4 loopback non-canonical' => ['127.1.2.3', true],
            'localhost' => ['localhost', true],
            'localhost with port' => ['localhost:3000', true],
            'localhost uppercase' => ['LOCALHOST', true],
            'ipv6 loopback bracketed' => ['[::1]', true],
            'ipv6 loopback bracketed with port' => ['[::1]:8080', true],
            'ipv6 loopback bare' => ['::1', true],
            'empty host (unix socket / relative)' => ['', true],
            'public dns name' => ['example.com', false],
            'public dns with port' => ['example.com:443', false],
            // Resolving a DNS name is network access, so even a name that
            // would resolve to loopback counts as remote.
            'dns name resolving to loopback' => ['localhost.localdomain', false],
            'public ipv4' => ['93.184.216.34', false],
            'public ipv6' => ['[2606:2800:220:1:248:1893:25c8:1946]', false],
            'link-local is not loopback' => ['169.254.1.1', false],
            'private lan is not loopback' => ['192.168.1.10', false],
        ];
    }
}
