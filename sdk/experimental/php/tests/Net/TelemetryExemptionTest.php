<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Net;

use GuzzleHttp\Client;
use GuzzleHttp\Handler\MockHandler;
use GuzzleHttp\HandlerStack;
use GuzzleHttp\Psr7\Response;
use HopTop\Kit\Net\NetPolicy;
use HopTop\Kit\Telemetry\Sink\HttpsSink;
use PHPUnit\Framework\TestCase;

/**
 * Telemetry is logging-class egress: `--offline` stops network traffic
 * the user asked for, it is not a second consent gate on diagnostics.
 * Consent and the telemetry mode already govern whether anything is
 * emitted at all.
 *
 * The endpoint here is deliberately remote. Loopback is exempt from the
 * guard by design, so a 127.0.0.1 target would pass with or without the
 * exemption and could never fail.
 */
class TelemetryExemptionTest extends TestCase
{
    protected function setUp(): void
    {
        NetPolicy::reset();
    }

    protected function tearDown(): void
    {
        NetPolicy::reset();
    }

    public function testHttpsSinkShipsWhileOffline(): void
    {
        NetPolicy::setOffline(true);

        $seen = 0;
        $mock = new MockHandler([new Response(202)]);
        $stack = HandlerStack::create($mock);
        $stack->push(function (callable $next) use (&$seen) {
            return function ($request, array $options) use ($next, &$seen) {
                ++$seen;

                return $next($request, $options);
            };
        });

        // An ordinary Guzzle client, exactly as an adopter would build it:
        // no offline guard pushed onto the stack.
        $sink = new HttpsSink(
            'https://telemetry.example.com/v1/events',
            new Client(['handler' => $stack]),
            batchSize: 1,
        );

        $sink->enqueue(['schema_version' => '1', 'sdk_lang' => 'php']);
        $sink->flush();

        self::assertSame(
            1,
            $seen,
            'telemetry was suppressed while offline: logging-class egress must stay exempt'
        );
    }

    /**
     * The exemption must stay narrow: guarding a client still refuses
     * user-initiated remote traffic, so exempting telemetry does not
     * quietly exempt everything else.
     */
    public function testGuardStillRefusesOrdinaryTrafficWhileOffline(): void
    {
        NetPolicy::setOffline(true);

        $mock = new MockHandler([new Response(200)]);
        $stack = HandlerStack::create($mock);
        \HopTop\Kit\Net\OfflineGuard::push($stack);
        $client = new Client(['handler' => $stack]);

        $this->expectException(\HopTop\Kit\Net\OfflineException::class);
        $client->request('GET', 'https://api.example.com/v1/thing');
    }
}
