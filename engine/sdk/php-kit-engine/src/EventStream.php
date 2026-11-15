<?php

declare(strict_types=1);

namespace HopTop\KitEngine;

use HopTop\KitEngine\Exception\KitEngineException;
use WebSocket\Client;
use WebSocket\Exception\ConnectionTimeoutException;
use WebSocket\Message\Text;

/**
 * Blocking, reconnecting subscription to the engine `/events` socket.
 *
 * Lazy: nothing connects until iteration starts. Each iteration opens a
 * socket, sends one `subscribe` frame for the topic, and yields an
 * {@see Event} per matching `message` frame. `welcome`, `ack`, `error`
 * and unparseable frames are skipped. A dropped socket is reopened (and
 * resubscribed) after {@see $reconnectDelay} seconds, forever; only a
 * failed first connect throws. Stop with `break`, or call {@see close()}
 * from a signal handler or another callback: the loop wakes every
 * {@see $pollInterval} seconds to notice. With `$timeout` (seconds) the
 * iteration ends cleanly that long after it starts.
 *
 * @implements \IteratorAggregate<int,Event>
 */
final class EventStream implements \IteratorAggregate
{
    private bool $closed = false;
    private ?Client $ws = null;
    private ?float $until = null;

    public function __construct(
        private readonly string $url,
        public readonly string $topic = '*',
        private readonly ?string $token = null,
        private readonly float $reconnectDelay = 1.0,
        private readonly float $pollInterval = 0.25,
        private readonly ?float $timeout = null,
    ) {
    }

    /**
     * Hub pattern match: `*` one segment, `**` one or more. The bare
     * `*` default means every topic.
     */
    public static function matches(string $pattern, string $topic): bool
    {
        if ($pattern === '*') {
            return true;
        }

        return self::matchSegments(explode('.', $pattern), explode('.', $topic));
    }

    /**
     * @param list<string> $p
     * @param list<string> $t
     */
    private static function matchSegments(array $p, array $t): bool
    {
        if ($p === []) {
            return $t === [];
        }
        if ($t === []) {
            return false;
        }
        if ($p[0] === '**') {
            for ($i = 1; $i <= \count($t); $i++) {
                if (self::matchSegments(\array_slice($p, 1), \array_slice($t, $i))) {
                    return true;
                }
            }

            return false;
        }

        return ($p[0] === '*' || $p[0] === $t[0]) && self::matchSegments(\array_slice($p, 1), \array_slice($t, 1));
    }

    /** @return \Generator<int,Event> */
    public function getIterator(): \Generator
    {
        if (!class_exists(Client::class)) {
            throw new KitEngineException('events() needs phrity/websocket: composer require phrity/websocket');
        }
        $first = true;
        $this->until = $this->timeout !== null ? microtime(true) + $this->timeout : null;
        try {
            while (!$this->done()) {
                try {
                    $this->connect();
                } catch (\Throwable $e) {
                    if ($first) {
                        throw new KitEngineException("events connect: {$e->getMessage()}", 0, $e);
                    }
                    $this->backoff();
                    continue;
                }
                $first = false;
                yield from $this->read();
                $this->drop();
                $this->backoff();
            }
        } finally {
            $this->drop();
        }
    }

    /** End iteration; safe from a signal handler or inside the loop. */
    public function close(): void
    {
        $this->closed = true;
    }

    private function done(): bool
    {
        return $this->closed || ($this->until !== null && microtime(true) >= $this->until);
    }

    private function connect(): void
    {
        $ws = new Client($this->url);
        $ws->setTimeout($this->pollInterval);
        if ($this->token !== null && $this->token !== '') {
            $ws->addHeader('Authorization', 'Bearer ' . $this->token);
        }
        $ws->connect();
        $ws->send(new Text(json_encode(
            ['type' => 'subscribe', 'topic' => $this->topic === '*' ? '**' : $this->topic],
            JSON_THROW_ON_ERROR,
        )));
        $this->ws = $ws;
    }

    /** @return \Generator<int,Event> */
    private function read(): \Generator
    {
        while (!$this->done() && $this->ws !== null) {
            try {
                $msg = $this->ws->receive();
            } catch (ConnectionTimeoutException) {
                continue;
            } catch (\Throwable) {
                return;
            }
            if (!$msg instanceof Text) {
                continue;
            }
            $frame = json_decode($msg->getContent(), true);
            if (!\is_array($frame) || ($frame['type'] ?? null) !== 'message' || !\is_string($frame['topic'] ?? null)) {
                continue;
            }
            if (self::matches($this->topic, $frame['topic'])) {
                yield new Event($frame['topic'], $frame['payload'] ?? null);
            }
        }
    }

    private function drop(): void
    {
        $ws = $this->ws;
        $this->ws = null;
        try {
            $ws?->disconnect();
        } catch (\Throwable) {
        }
    }

    private function backoff(): void
    {
        $deadline = microtime(true) + $this->reconnectDelay;
        while (!$this->done() && ($left = $deadline - microtime(true)) > 0) {
            usleep((int) (min($left, $this->pollInterval) * 1_000_000));
        }
    }
}
