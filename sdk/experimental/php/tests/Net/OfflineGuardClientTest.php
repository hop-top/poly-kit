<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Net;

use GuzzleHttp\Psr7\Request;
use GuzzleHttp\Psr7\Response;
use HopTop\Kit\Net\NetPolicy;
use HopTop\Kit\Net\OfflineException;
use HopTop\Kit\Net\OfflineGuardClient;
use PHPUnit\Framework\TestCase;

/**
 * The PSR-18 half of enforcement, for adopters who inject a client that
 * is not Guzzle. The handler-stack guard cannot reach those, so this
 * decorator wraps any PSR-18 client at its single `sendRequest` seam.
 */
class OfflineGuardClientTest extends TestCase
{
    protected function setUp(): void
    {
        NetPolicy::reset();
    }

    protected function tearDown(): void
    {
        NetPolicy::reset();
    }

    /**
     * Records whether the inner client was ever reached, so a blocked
     * test can assert the guard stopped short of it rather than merely
     * that an exception surfaced.
     */
    private function innerClient(): RecordingClient
    {
        return new RecordingClient();
    }

    public function testBlocksExternalHostWhenOffline(): void
    {
        NetPolicy::setOffline(true);
        $inner = $this->innerClient();
        $client = new OfflineGuardClient($inner);

        try {
            $client->sendRequest(new Request('GET', 'https://example.invalid/x'));
            $this->fail('expected OfflineException');
        } catch (OfflineException) {
            $this->assertFalse($inner->called, 'inner client was reached');
        }
    }

    public function testAllowsLoopbackWhenOffline(): void
    {
        NetPolicy::setOffline(true);
        $client = new OfflineGuardClient($this->innerClient());

        $res = $client->sendRequest(new Request('GET', 'http://127.0.0.1:8080/x'));
        $this->assertSame(200, $res->getStatusCode());
    }

    public function testTreatsDnsNameAsRemoteWhenOffline(): void
    {
        NetPolicy::setOffline(true);
        $client = new OfflineGuardClient($this->innerClient());

        $this->expectException(OfflineException::class);
        $client->sendRequest(new Request('GET', 'http://localhost.localdomain/x'));
    }

    public function testPassesThroughWhenOnline(): void
    {
        $inner = $this->innerClient();
        $client = new OfflineGuardClient($inner);

        $res = $client->sendRequest(new Request('GET', 'https://example.invalid/x'));
        $this->assertSame(200, $res->getStatusCode());
        $this->assertTrue($inner->called);
    }

    /**
     * PSR-18 requires sendRequest to throw only
     * ClientExceptionInterface, so the typed offline error must satisfy
     * that contract or it breaks conforming callers' catch blocks.
     */
    public function testOfflineExceptionSatisfiesPsr18ClientException(): void
    {
        NetPolicy::setOffline(true);
        $client = new OfflineGuardClient($this->innerClient());

        $this->expectException(\Psr\Http\Client\ClientExceptionInterface::class);
        $client->sendRequest(new Request('GET', 'https://example.invalid/x'));
    }

    public function testWrapIsIdempotent(): void
    {
        $inner = $this->innerClient();
        $once = OfflineGuardClient::wrap($inner);
        $twice = OfflineGuardClient::wrap($once);

        $this->assertSame($once, $twice);
    }
}
