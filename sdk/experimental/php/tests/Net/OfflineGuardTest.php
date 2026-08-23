<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Net;

use GuzzleHttp\Client;
use GuzzleHttp\Handler\MockHandler;
use GuzzleHttp\HandlerStack;
use GuzzleHttp\Psr7\Request;
use GuzzleHttp\Psr7\Response;
use HopTop\Kit\Net\NetPolicy;
use HopTop\Kit\Net\OfflineException;
use HopTop\Kit\Net\OfflineGuard;
use PHPUnit\Framework\TestCase;

/**
 * Enforcement, at the Guzzle handler-stack chokepoint.
 *
 * Guzzle's four public entry points (`request`, `send`, `requestAsync`,
 * `sendAsync`) are siblings — none delegates to another — but all four
 * funnel into `transfer()`, which runs the handler stack. Guarding the
 * stack is therefore the PHP analogue of guarding Go's
 * http.RoundTripper: it sits beneath every entry point, so a caller
 * cannot route around it by picking a different method.
 */
class OfflineGuardTest extends TestCase
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
     * A stack that would return 200 if the request ever reached the
     * network. Any test asserting a block proves the guard fired, not
     * that the transport happened to fail.
     */
    private function guardedClient(): Client
    {
        $stack = HandlerStack::create(new MockHandler([
            new Response(200, [], 'reached the network'),
            new Response(200, [], 'reached the network'),
            new Response(200, [], 'reached the network'),
            new Response(200, [], 'reached the network'),
        ]));
        OfflineGuard::push($stack);

        return new Client(['handler' => $stack]);
    }

    public function testBlocksExternalHostWhenOffline(): void
    {
        NetPolicy::setOffline(true);
        $client = $this->guardedClient();

        $this->expectException(OfflineException::class);
        $client->request('GET', 'https://example.invalid/x');
    }

    public function testBlockedExceptionNamesMethodAndUrl(): void
    {
        NetPolicy::setOffline(true);
        $client = $this->guardedClient();

        try {
            $client->request('GET', 'https://example.invalid/x');
            $this->fail('expected OfflineException');
        } catch (OfflineException $e) {
            $this->assertStringContainsString('GET', $e->getMessage());
            $this->assertStringContainsString('example.invalid', $e->getMessage());
            $this->assertStringContainsString('--offline', $e->getMessage());
        }
    }

    /**
     * Credentials in the URL must not leak into the error message, which
     * is printed and logged. Mirrors Go's use of URL.Redacted().
     */
    public function testBlockedExceptionRedactsCredentials(): void
    {
        NetPolicy::setOffline(true);
        $client = $this->guardedClient();

        try {
            $client->request('GET', 'https://user:hunter2@example.invalid/x');
            $this->fail('expected OfflineException');
        } catch (OfflineException $e) {
            $this->assertStringNotContainsString('hunter2', $e->getMessage());
        }
    }

    public function testAllowsLoopbackWhenOffline(): void
    {
        NetPolicy::setOffline(true);
        $client = $this->guardedClient();

        $res = $client->request('GET', 'http://127.0.0.1:8080/x');
        $this->assertSame(200, $res->getStatusCode());
    }

    public function testAllowsLocalhostWhenOffline(): void
    {
        NetPolicy::setOffline(true);
        $client = $this->guardedClient();

        $res = $client->request('GET', 'http://localhost:3000/x');
        $this->assertSame(200, $res->getStatusCode());
    }

    public function testAllowsIpv6LoopbackWhenOffline(): void
    {
        NetPolicy::setOffline(true);
        $client = $this->guardedClient();

        $res = $client->request('GET', 'http://[::1]:8080/x');
        $this->assertSame(200, $res->getStatusCode());
    }

    /**
     * Resolving a DNS name is itself network access, so a hostname is
     * remote even when it would resolve to loopback.
     */
    public function testTreatsDnsNameAsRemoteWhenOffline(): void
    {
        NetPolicy::setOffline(true);
        $client = $this->guardedClient();

        $this->expectException(OfflineException::class);
        $client->request('GET', 'http://localhost.localdomain/x');
    }

    public function testAllowsExternalHostWhenOnline(): void
    {
        $client = $this->guardedClient();

        $res = $client->request('GET', 'https://example.invalid/x');
        $this->assertSame(200, $res->getStatusCode());
        $this->assertSame('reached the network', (string) $res->getBody());
    }

    /**
     * The guard must sit beneath every Guzzle entry point, not just the
     * one kit happens to call. `send()` and `request()` are siblings, so
     * a decorator wrapping only one of them would leave a bypass.
     */
    public function testBlocksViaSendEntryPoint(): void
    {
        NetPolicy::setOffline(true);
        $client = $this->guardedClient();

        $this->expectException(OfflineException::class);
        $client->send(new Request('POST', 'https://example.invalid/x'));
    }

    public function testBlocksViaSendRequestPsr18EntryPoint(): void
    {
        NetPolicy::setOffline(true);
        $client = $this->guardedClient();

        $this->expectException(OfflineException::class);
        $client->sendRequest(new Request('GET', 'https://example.invalid/x'));
    }

    public function testBlocksViaAsyncEntryPoint(): void
    {
        NetPolicy::setOffline(true);
        $client = $this->guardedClient();

        $this->expectException(OfflineException::class);
        $client->requestAsync('GET', 'https://example.invalid/x')->wait();
    }

    /** Pushing the guard twice must not double-block or double-wrap. */
    public function testPushIsIdempotent(): void
    {
        $stack = HandlerStack::create(new MockHandler([new Response(200)]));
        OfflineGuard::push($stack);
        OfflineGuard::push($stack);
        $client = new Client(['handler' => $stack]);

        $res = $client->request('GET', 'https://example.invalid/x');
        $this->assertSame(200, $res->getStatusCode());
    }
}
