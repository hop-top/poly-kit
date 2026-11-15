<?php

declare(strict_types=1);

namespace HopTop\KitEngine\Tests;

use HopTop\KitEngine\Event;
use HopTop\KitEngine\EventStream;
use HopTop\KitEngine\Exception\KitEngineException;
use HopTop\KitEngine\KitEngine;
use HopTop\KitEngine\Tests\Support\RecordingClient;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;

final class EventStreamTest extends TestCase
{
    private string $dir;
    /** @var resource|null */
    private $server;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/kit-events-php-' . bin2hex(random_bytes(6));
        mkdir($this->dir);
    }

    protected function tearDown(): void
    {
        if (\is_resource($this->server)) {
            proc_terminate($this->server);
            proc_close($this->server);
        }
        foreach (glob($this->dir . '/*') ?: [] as $f) {
            unlink($f);
        }
        rmdir($this->dir);
    }

    private static function msg(string $topic, mixed $payload = null): array
    {
        return ['type' => 'message', 'topic' => $topic, 'payload' => $payload];
    }

    /** Start the scripted server; returns its ws:// URL. */
    private function serve(array $conns): string
    {
        $sock = stream_socket_server('tcp://127.0.0.1:0', $errno, $err);
        self::assertNotFalse($sock);
        $port = (int) substr((string) strrchr((string) stream_socket_get_name($sock, false), ':'), 1);
        fclose($sock);

        file_put_contents("{$this->dir}/script.json", json_encode(['conns' => $conns]));
        touch("{$this->dir}/log");
        $pipes = [];
        $proc = proc_open(
            [PHP_BINARY, __DIR__ . '/Support/ws_server.php', (string) $port, "{$this->dir}/script.json", "{$this->dir}/log"],
            [0 => ['file', '/dev/null', 'r'], 1 => ['file', '/dev/null', 'w'], 2 => ['file', "{$this->dir}/err", 'w']],
            $pipes,
        );
        self::assertIsResource($proc);
        $this->server = $proc;
        for ($i = 0; $i < 100; $i++) {
            $c = @fsockopen('127.0.0.1', $port, $en, $es, 0.1);
            if ($c !== false) {
                fclose($c);

                return "ws://127.0.0.1:{$port}/events";
            }
            usleep(50_000);
        }
        self::fail('ws server did not start');
    }

    /** @return list<array<string,mixed>> */
    private function log(): array
    {
        return array_map(
            static fn (string $l) => json_decode($l, true),
            file("{$this->dir}/log", FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) ?: [],
        );
    }

    /** Collect up to $n events, then stop. @return list<Event> */
    private function take(EventStream $s, int $n): array
    {
        $out = [];
        $deadline = microtime(true) + 10;
        foreach ($s as $e) {
            $out[] = $e;
            if (\count($out) >= $n || microtime(true) > $deadline) {
                break;
            }
        }

        return $out;
    }

    public function testConnectsWithAuthAndSubscribes(): void
    {
        $url = $this->serve([['frames' => [self::msg('a.b', ['id' => '1'])]]]);
        $events = $this->take(new EventStream($url, 'a.b', 'tok'), 1);

        self::assertCount(1, $events);
        $log = $this->log();
        self::assertSame(['path' => '/events', 'auth' => 'Bearer tok'], $log[0]['open']);
        self::assertSame(['type' => 'subscribe', 'topic' => 'a.b'], $log[1]['recv']);
    }

    public function testNoAuthHeaderWithoutToken(): void
    {
        $url = $this->serve([['frames' => [self::msg('a.b')]]]);
        $this->take(new EventStream($url, 'a.b'), 1);

        self::assertSame('', $this->log()[0]['open']['auth']);
    }

    public function testDecodesFramesAndSkipsControlFrames(): void
    {
        $url = $this->serve([['frames' => [
            ['type' => 'error', 'topic' => 'x'],
            'not json',
            self::msg('kit.engine.document.created', ['type' => 'notes', 'id' => 'n1', 'seq' => 3]),
        ]]]);
        $events = $this->take(new EventStream($url), 1);

        self::assertSame('kit.engine.document.created', $events[0]->topic);
        self::assertSame(['type' => 'notes', 'id' => 'n1', 'seq' => 3], $events[0]->payload);
    }

    public function testBareStarSubscribesToEverything(): void
    {
        $url = $this->serve([['frames' => [self::msg('a.b.c.d'), self::msg('x')]]]);
        $events = $this->take(new EventStream($url, '*'), 2);

        self::assertSame(['a.b.c.d', 'x'], array_map(static fn (Event $e) => $e->topic, $events));
        self::assertSame('**', $this->log()[1]['recv']['topic']);
    }

    public function testTopicFilterDropsNonMatching(): void
    {
        $url = $this->serve([['frames' => [
            self::msg('notes.created'),
            self::msg('tasks.created'),
            self::msg('notes.deleted'),
            self::msg('notes.a.b'),
        ]]]);
        $events = $this->take(new EventStream($url, 'notes.*'), 2);

        self::assertSame(['notes.created', 'notes.deleted'], array_map(static fn (Event $e) => $e->topic, $events));
    }

    /** @return array<string,array{string,string,bool}> */
    public static function patterns(): array
    {
        return [
            'exact' => ['a.b', 'a.b', true],
            'exact miss' => ['a.b', 'a.c', false],
            'star one segment' => ['a.*', 'a.b', true],
            'star not two' => ['a.*', 'a.b.c', false],
            'star not zero' => ['a.*', 'a', false],
            'doublestar many' => ['a.**', 'a.b.c', true],
            'doublestar needs one' => ['a.**', 'a', false],
            'doublestar middle' => ['a.**.z', 'a.b.c.z', true],
            'bare star all' => ['*', 'a.b.c.d', true],
        ];
    }

    #[DataProvider('patterns')]
    public function testMatches(string $pattern, string $topic, bool $want): void
    {
        self::assertSame($want, EventStream::matches($pattern, $topic));
    }

    public function testReconnectsAfterDropAndResubscribes(): void
    {
        $url = $this->serve([
            ['frames' => [self::msg('a.1')], 'drop' => true],
            ['frames' => [self::msg('a.2')]],
        ]);
        $events = $this->take(new EventStream($url, 'a.*', null, 0.05), 2);

        self::assertSame(['a.1', 'a.2'], array_map(static fn (Event $e) => $e->topic, $events));
        $opens = array_filter($this->log(), static fn (array $r) => isset($r['open']));
        $subs = array_filter($this->log(), static fn (array $r) => isset($r['recv']));
        self::assertCount(2, $opens);
        self::assertCount(2, $subs);
    }

    public function testTimeoutEndsIdleStream(): void
    {
        $url = $this->serve([['frames' => []]]);
        $start = microtime(true);
        $n = 0;
        foreach (new EventStream($url, '*', null, 0.05, 0.05, 0.4) as $_) {
            $n++;
        }

        self::assertSame(0, $n);
        self::assertGreaterThanOrEqual(0.4, microtime(true) - $start);
        self::assertLessThan(3, microtime(true) - $start);
    }

    public function testCloseStopsIdleStream(): void
    {
        if (!\function_exists('pcntl_alarm')) {
            self::markTestSkipped('pcntl missing');
        }
        $url = $this->serve([['frames' => []]]);
        $stream = new EventStream($url, '*', null, 0.05, 0.05);
        pcntl_async_signals(true);
        pcntl_signal(SIGALRM, static fn () => $stream->close());
        pcntl_alarm(1);
        $start = microtime(true);
        foreach ($stream as $_) {
            self::fail('no events expected');
        }
        pcntl_signal(SIGALRM, SIG_DFL);

        self::assertLessThan(4, microtime(true) - $start);
    }

    public function testBreakDisconnects(): void
    {
        $url = $this->serve([['frames' => [self::msg('a.1'), self::msg('a.2')]]]);
        $this->take(new EventStream($url, 'a.*'), 1);

        $gone = false;
        for ($i = 0; $i < 40 && !$gone; $i++) {
            usleep(50_000);
            $gone = array_filter($this->log(), static fn (array $r) => isset($r['gone'])) !== [];
        }
        self::assertTrue($gone, 'server saw the socket close after break');
    }

    public function testFirstConnectFailureThrows(): void
    {
        $sock = stream_socket_server('tcp://127.0.0.1:0');
        $port = (int) substr((string) strrchr((string) stream_socket_get_name($sock, false), ':'), 1);
        fclose($sock);

        $this->expectException(KitEngineException::class);
        $this->expectExceptionMessage('events connect');
        foreach (new EventStream("ws://127.0.0.1:{$port}/events") as $_) {
        }
    }

    public function testEngineEventsBuildsStreamForPortAndToken(): void
    {
        $url = $this->serve([['frames' => [self::msg('a.b')]]]);
        $port = (int) parse_url($url, PHP_URL_PORT);
        $engine = KitEngine::connect($port, 'tok', null, new RecordingClient(static fn () => RecordingClient::json(200, ['pid' => 1])));

        $events = $this->take($engine->events('a.b'), 1);

        self::assertSame('a.b', $events[0]->topic);
        self::assertSame('Bearer tok', $this->log()[0]['open']['auth']);
    }

    public function testRealKitEvents(): void
    {
        $bin = getenv('KIT_BIN');
        if ($bin === false || $bin === '') {
            self::markTestSkipped('KIT_BIN not set');
        }
        if (!\function_exists('pcntl_alarm')) {
            self::markTestSkipped('pcntl missing');
        }
        $engine = KitEngine::start(['binPath' => $bin, 'app' => 'php-events-test', 'data' => $this->dir, 'noPeer' => true, 'noSync' => true]);
        try {
            pcntl_async_signals(true);
            pcntl_signal(SIGALRM, static fn () => $engine->collection('notes')->create(['title' => 'x']));
            pcntl_alarm(1);
            $topics = [];
            $start = microtime(true);
            try {
                foreach ($engine->events('kit.engine.document.**', 3.0) as $e) {
                    $topics[] = $e->topic;
                }
            } catch (KitEngineException $e) {
                if (str_contains($e->getMessage(), '501')) {
                    self::markTestSkipped('kit binary predates /events');
                }
                throw $e;
            }
            $elapsed = microtime(true) - $start;
            pcntl_signal(SIGALRM, SIG_DFL);
        } finally {
            $engine->stop();
        }
        self::assertGreaterThanOrEqual(2.9, $elapsed, 'socket stayed up until the timeout');
        // The engine does not bridge document events to the socket yet
        // (protocol doc): a clean, event-free end is the expected result;
        // once wired, every yielded topic must be a document topic.
        foreach ($topics as $t) {
            self::assertStringStartsWith('kit.engine.document.', $t);
        }
    }
}
