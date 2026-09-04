<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Telemetry\Sink;

use GuzzleHttp\Client;
use GuzzleHttp\Exception\ConnectException;
use GuzzleHttp\Handler\MockHandler;
use GuzzleHttp\HandlerStack;
use GuzzleHttp\Middleware;
use GuzzleHttp\Psr7\Request;
use GuzzleHttp\Psr7\Response;
use HopTop\Kit\Telemetry\Sink\HttpsSink;
use PHPUnit\Framework\TestCase;

class HttpsSinkTest extends TestCase
{
    /**
     * @param array<int, Response|\Throwable> $responses
     * @param array<int, array<string, mixed>> $transactions
     */
    private function clientWith(array $responses, array &$transactions = []): Client
    {
        $mock = new MockHandler($responses);
        $stack = HandlerStack::create($mock);
        $stack->push(Middleware::history($transactions));

        return new Client(['handler' => $stack]);
    }

    public function testHappyPath202(): void
    {
        $transactions = [];
        $client = $this->clientWith([new Response(202)], $transactions);

        $sink = new HttpsSink('https://ingest.example/v1/events', $client);
        $sink->enqueue(['event' => 'a']);
        $sink->enqueue(['event' => 'b']);
        $sink->flush();

        $stats = $sink->stats();
        $this->assertSame(2, $stats['emitted']);
        $this->assertSame(0, $stats['dropped']);
        $this->assertSame(0, $stats['queued']);
        $this->assertSame('https://ingest.example/v1/events', $stats['endpoint']);

        $this->assertCount(1, $transactions);
        /** @var Request $req */
        $req = $transactions[0]['request'];
        $this->assertSame('POST', $req->getMethod());
        $this->assertSame('application/x-ndjson', $req->getHeaderLine('Content-Type'));

        $body = (string) $req->getBody();
        $this->assertSame(
            "{\"event\":\"a\"}\n{\"event\":\"b\"}\n",
            $body,
        );
    }

    public function testFiveHundredTriggersOneRetry(): void
    {
        $transactions = [];
        $client = $this->clientWith(
            [new Response(503), new Response(202)],
            $transactions,
        );

        $sink = new HttpsSink('https://ingest.example/v1', $client);
        $sink->enqueue(['event' => 'r']);
        $sink->flush();

        $this->assertCount(2, $transactions);
        $this->assertSame(1, $sink->stats()['emitted']);
        $this->assertSame(0, $sink->stats()['dropped']);
    }

    public function testFiveHundredRetryAlsoFailsDrops(): void
    {
        $transactions = [];
        $client = $this->clientWith(
            [new Response(503), new Response(500)],
            $transactions,
        );

        $sink = new HttpsSink('https://ingest.example/v1', $client);
        $sink->enqueue(['event' => 'd']);
        $sink->flush();

        $this->assertCount(2, $transactions);
        $this->assertSame(0, $sink->stats()['emitted']);
        $this->assertSame(1, $sink->stats()['dropped']);
    }

    public function testFourHundredDropsImmediately(): void
    {
        $transactions = [];
        $client = $this->clientWith([new Response(400)], $transactions);

        $sink = new HttpsSink('https://ingest.example/v1', $client);
        $sink->enqueue(['event' => 'bad']);
        $sink->flush();

        // No retry on 4xx.
        $this->assertCount(1, $transactions);
        $this->assertSame(0, $sink->stats()['emitted']);
        $this->assertSame(1, $sink->stats()['dropped']);
    }

    public function testTransportExceptionDoesNotPropagate(): void
    {
        $client = $this->clientWith([
            new ConnectException(
                'simulated connect timeout',
                new Request('POST', 'https://ingest.example/v1'),
            ),
        ]);

        $sink = new HttpsSink('https://ingest.example/v1', $client);
        $sink->enqueue(['event' => 'x']);

        // Must not throw.
        $sink->flush();

        $this->assertSame(0, $sink->stats()['emitted']);
        $this->assertSame(1, $sink->stats()['dropped']);
    }

    public function testBatchingByBatchSize(): void
    {
        $transactions = [];
        $client = $this->clientWith(
            [new Response(202), new Response(202)],
            $transactions,
        );

        $sink = new HttpsSink('https://ingest.example/v1', $client, batchSize: 2);
        for ($i = 0; $i < 4; $i++) {
            $sink->enqueue(['n' => $i]);
        }
        $sink->flush();

        $this->assertCount(2, $transactions);
        $this->assertSame(4, $sink->stats()['emitted']);
    }

    public function testQueueCapDrops(): void
    {
        $sink = new HttpsSink(
            'https://ingest.example/v1',
            $this->clientWith([]),
            cap: 2,
        );
        $sink->enqueue(['n' => 1]);
        $sink->enqueue(['n' => 2]);
        $sink->enqueue(['n' => 3]);
        $sink->enqueue(['n' => 4]);

        $this->assertSame(2, $sink->stats()['queued']);
        $this->assertSame(2, $sink->stats()['dropped']);
    }

    public function testFlushEmptyNoOp(): void
    {
        $transactions = [];
        $sink = new HttpsSink('https://ingest.example/v1', $this->clientWith([], $transactions));
        $sink->flush();
        $this->assertCount(0, $transactions);
        $this->assertSame(0, $sink->stats()['emitted']);
    }

    public function testRedirectNotFollowedSoBatchNeverReachesAttacker(): void
    {
        $transactions = [];
        // Ingestor answers 302 pointing at an attacker-controlled host. Under
        // Guzzle's default allow_redirects the NDJSON body — which carries
        // install_id — would be re-sent there.
        $client = $this->clientWith(
            [
                new Response(302, ['Location' => 'https://attacker.example/collect']),
                new Response(202),
            ],
            $transactions,
        );

        $sink = new HttpsSink('https://ingest.example/v1', $client);
        $sink->enqueue(['event' => 'secret', 'install_id' => 'inst_123']);
        $sink->flush();

        // Exactly one request: the redirect must not be followed.
        $this->assertCount(1, $transactions);

        foreach ($transactions as $tx) {
            /** @var Request $req */
            $req = $tx['request'];
            $this->assertNotSame(
                'attacker.example',
                $req->getUri()->getHost(),
                'telemetry batch must never be sent to a redirect target',
            );
        }

        // 302 is neither 2xx nor 5xx, so the batch drops rather than emitting.
        $this->assertSame(0, $sink->stats()['emitted']);
        $this->assertSame(1, $sink->stats()['dropped']);
    }

    public function testRetryPathAlsoRefusesRedirects(): void
    {
        $transactions = [];
        // 5xx routes through retryBatch(); its options must be hardened too.
        $client = $this->clientWith(
            [
                new Response(503),
                new Response(302, ['Location' => 'https://attacker.example/collect']),
                new Response(202),
            ],
            $transactions,
        );

        $sink = new HttpsSink('https://ingest.example/v1', $client);
        $sink->enqueue(['event' => 'secret', 'install_id' => 'inst_123']);
        $sink->flush();

        // Original plus one retry. The retry's 302 must not spawn a third request.
        $this->assertCount(2, $transactions);

        foreach ($transactions as $tx) {
            /** @var Request $req */
            $req = $tx['request'];
            $this->assertSame('ingest.example', $req->getUri()->getHost());
        }

        $this->assertSame(0, $sink->stats()['emitted']);
        $this->assertSame(1, $sink->stats()['dropped']);
    }
}
