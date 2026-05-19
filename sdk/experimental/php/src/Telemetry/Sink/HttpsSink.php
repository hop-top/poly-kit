<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry\Sink;

use GuzzleHttp\ClientInterface;
use GuzzleHttp\Psr7\Request;
use Throwable;

/**
 * Opt-in HTTPS sink: POST batched NDJSON to a remote ingestor.
 *
 * # FPM caveat
 *
 * Under php-fpm, `flush()` performs synchronous HTTPS calls. If you
 * invoke this sink during a request, the request latency includes the
 * round-trip to the ingestor. Two mitigations:
 *
 *   1. Don't register a shutdown flush in FPM. This class does NOT
 *      auto-register one for that reason. Adopters who want shutdown
 *      flushing in long-running CLIs can call
 *      `register_shutdown_function([$sink, 'flush'])` themselves.
 *   2. Prefer the JSONL sink (default) and let a separate drain ship
 *      events asynchronously. HTTPS-inline is a deliberate opt-in for
 *      contexts where the additional latency is acceptable.
 *
 * Retry policy: one retry on 5xx; 4xx and transport errors drop
 * immediately. Both timeouts are wall-clock; the underlying Guzzle
 * client controls connect / total budgets via the options below.
 *
 * Body shape: `application/x-ndjson` — one JSON envelope per line.
 */
final class HttpsSink implements SinkInterface
{
    /** @var array<int, array<string, mixed>> */
    private array $queue = [];

    private int $emitted = 0;
    private int $dropped = 0;

    public function __construct(
        private readonly string $endpoint,
        private readonly ClientInterface $client,
        private readonly int $cap = 1024,
        private readonly int $batchSize = 50,
        private readonly int $connectTimeoutS = 5,
        private readonly int $totalTimeoutS = 10,
    ) {
    }

    public function enqueue(array $envelope): void
    {
        if (count($this->queue) >= $this->cap) {
            $this->dropped++;

            return;
        }
        $this->queue[] = $envelope;
    }

    public function flush(): void
    {
        if (empty($this->queue)) {
            return;
        }

        $batches = array_chunk($this->queue, $this->batchSize);
        $this->queue = [];

        foreach ($batches as $batch) {
            $this->sendBatch($batch);
        }
    }

    public function stats(): array
    {
        return [
            'emitted' => $this->emitted,
            'dropped' => $this->dropped,
            'queued' => count($this->queue),
            'endpoint' => $this->endpoint,
        ];
    }

    /**
     * @param array<int, array<string, mixed>> $batch
     */
    private function sendBatch(array $batch): void
    {
        $body = '';
        foreach ($batch as $envelope) {
            $line = json_encode($envelope, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
            if ($line === false) {
                $this->dropped++;
                continue;
            }
            $body .= $line . "\n";
        }
        if ($body === '') {
            return;
        }

        $request = new Request(
            'POST',
            $this->endpoint,
            ['Content-Type' => 'application/x-ndjson'],
            $body,
        );
        $options = [
            'connect_timeout' => $this->connectTimeoutS,
            'timeout' => $this->totalTimeoutS,
            // Inspect status codes ourselves: 4xx → drop, 5xx → one retry.
            // Guzzle's default would throw on these and skip the retry path.
            'http_errors' => false,
        ];

        try {
            $response = $this->client->send($request, $options);
            $status = $response->getStatusCode();
            if ($status >= 200 && $status < 300) {
                $this->emitted += count($batch);

                return;
            }
            if ($status >= 500) {
                $this->retryBatch($request, $options, $batch);

                return;
            }
            // 4xx: client-side problem, immediate drop.
            $this->dropped += count($batch);
        } catch (Throwable) {
            // Transport failure (connect timeout, DNS, TLS, ...). Drop.
            // Per ADR-0038, telemetry is best-effort and never fatal.
            $this->dropped += count($batch);
        }
    }

    /**
     * @param array<int, array<string, mixed>> $batch
     * @param array<string, mixed> $options
     */
    private function retryBatch(Request $request, array $options, array $batch): void
    {
        try {
            $response = $this->client->send($request, $options);
            $status = $response->getStatusCode();
            if ($status >= 200 && $status < 300) {
                $this->emitted += count($batch);

                return;
            }
            $this->dropped += count($batch);
        } catch (Throwable) {
            $this->dropped += count($batch);
        }
    }
}
