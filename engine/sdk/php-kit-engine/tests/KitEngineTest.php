<?php

declare(strict_types=1);

namespace HopTop\KitEngine\Tests;

use GuzzleHttp\Psr7\Response;
use HopTop\KitEngine\Exception\HttpException;
use HopTop\KitEngine\Exception\KitEngineException;
use HopTop\KitEngine\IdentityClient;
use HopTop\KitEngine\KitEngine;
use HopTop\KitEngine\PeerClient;
use HopTop\KitEngine\SyncClient;
use HopTop\KitEngine\Tests\Support\RecordingClient;
use PHPUnit\Framework\TestCase;
use Psr\Http\Message\RequestInterface;

final class KitEngineTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/kit-engine-php-' . bin2hex(random_bytes(6));
        mkdir($this->dir);
    }

    protected function tearDown(): void
    {
        foreach (glob($this->dir . '/*') ?: [] as $f) {
            unlink($f);
        }
        rmdir($this->dir);
    }

    /**
     * Fake `kit`: records argv, prints $stdout, then idles until the
     * marker file appears (created by the mock shutdown handler).
     */
    private function fakeKit(string $stdout): string
    {
        $bin = $this->dir . '/kit';
        $argv = var_export($this->dir . '/argv.json', true);
        $marker = var_export($this->dir . '/stop', true);
        $out = var_export($stdout, true);
        file_put_contents($bin, <<<PHP
        #!/usr/bin/env php
        <?php
        file_put_contents({$argv}, json_encode(array_slice(\$argv, 1)));
        file_put_contents(dirname({$argv}) . '/pid', (string) getmypid());
        if ({$out} !== '') { fwrite(STDOUT, {$out}); fflush(STDOUT); }
        while (!file_exists({$marker})) { usleep(20000); }
        PHP);
        chmod($bin, 0755);

        return $bin;
    }

    private function healthyClient(): RecordingClient
    {
        $marker = $this->dir . '/stop';

        return new RecordingClient(function (RequestInterface $r) use ($marker) {
            if ($r->getUri()->getPath() === '/shutdown') {
                touch($marker);

                return new Response(204);
            }

            return RecordingClient::json(200, ['status' => 'ok', 'pid' => 4242]);
        });
    }

    private const STARTUP = '{"port":9876,"pid":1234,"token":"tok","shutdown_token":"stok"}' . "\n";

    public function testStartParsesStartupJsonAndGatesOnHealth(): void
    {
        $client = $this->healthyClient();
        $engine = KitEngine::start([
            'binPath' => $this->fakeKit(self::STARTUP),
            'client' => $client,
        ]);

        self::assertSame(9876, $engine->port);
        self::assertSame(1234, $engine->pid);
        self::assertSame('tok', $engine->token);
        self::assertSame('stok', $engine->shutdownToken);
        self::assertSame('GET', $client->requests[0]->getMethod());
        self::assertSame('http://127.0.0.1:9876/health', (string) $client->requests[0]->getUri());

        $engine->stop();
    }

    public function testStartPassesFlags(): void
    {
        $engine = KitEngine::start([
            'binPath' => $this->fakeKit(self::STARTUP),
            'client' => $this->healthyClient(),
            'app' => 'myapp',
            'data' => '/tmp/d',
            'port' => 0,
            'daemon' => true,
            'encrypt' => true,
            'noPeer' => true,
            'noSync' => true,
        ]);
        $argv = json_decode((string) file_get_contents($this->dir . '/argv.json'), true);
        $engine->stop();

        self::assertSame(
            ['serve', '--port', '0', '--app', 'myapp', '--data', '/tmp/d', '--daemon', '--encrypt', '--no-peer', '--no-sync'],
            $argv,
        );
    }

    public function testStartOmitsUnsetFlags(): void
    {
        $engine = KitEngine::start(['binPath' => $this->fakeKit(self::STARTUP), 'client' => $this->healthyClient()]);
        $argv = json_decode((string) file_get_contents($this->dir . '/argv.json'), true);
        $engine->stop();

        self::assertSame(['serve', '--port', '0'], $argv);
    }

    public function testStartRejectsInvalidStartupJson(): void
    {
        $this->expectException(KitEngineException::class);
        $this->expectExceptionMessage('invalid startup JSON');
        KitEngine::start(['binPath' => $this->fakeKit("not json\n"), 'client' => $this->healthyClient()]);
    }

    public function testStartFailsWhenProcessExitsWithoutOutput(): void
    {
        $bin = $this->dir . '/kit';
        file_put_contents($bin, "#!/bin/sh\nexit 3\n");
        chmod($bin, 0755);

        $this->expectException(KitEngineException::class);
        $this->expectExceptionMessage('exited');
        KitEngine::start(['binPath' => $bin, 'client' => $this->healthyClient()]);
    }

    public function testStartFailsWhenBinaryMissing(): void
    {
        $this->expectException(KitEngineException::class);
        KitEngine::start(['binPath' => $this->dir . '/nope', 'client' => $this->healthyClient()]);
    }

    public function testStartTimesOutWithoutStartupLine(): void
    {
        $this->expectException(KitEngineException::class);
        $this->expectExceptionMessage('timed out');
        try {
            KitEngine::start([
                'binPath' => $this->fakeKit(''),
                'client' => $this->healthyClient(),
                'startTimeout' => 0.3,
            ]);
        } finally {
            touch($this->dir . '/stop'); // let the fake exit if it survived
        }
    }

    public function testStartFailsAndReapsOnUnhealthy(): void
    {
        $client = new RecordingClient(static fn () => RecordingClient::json(503, ['status' => 'down']));

        try {
            KitEngine::start([
                'binPath' => $this->fakeKit(self::STARTUP),
                'client' => $client,
                'healthTimeout' => 0.3,
            ]);
            self::fail('expected health failure');
        } catch (KitEngineException $e) {
            self::assertStringContainsString('health check failed', $e->getMessage());
            self::assertGreaterThan(1, \count($client->requests), 'health gate retries');
        } finally {
            touch($this->dir . '/stop');
        }
    }

    public function testConnectChecksHealthAndReadsPid(): void
    {
        $client = new RecordingClient(static fn () => RecordingClient::json(200, ['status' => 'ok', 'pid' => 77]));
        $engine = KitEngine::connect(9090, 't', 's', $client);

        self::assertSame(9090, $engine->port);
        self::assertSame(77, $engine->pid);
        self::assertSame('t', $engine->token);
        self::assertSame('s', $engine->shutdownToken);
        self::assertSame('http://127.0.0.1:9090/health', (string) $client->last()->getUri());
    }

    public function testConnectThrowsOnUnhealthy(): void
    {
        $client = new RecordingClient(static fn () => RecordingClient::json(500, []));
        $this->expectException(HttpException::class);
        KitEngine::connect(9090, null, null, $client);
    }

    public function testStopPostsShutdownWithShutdownTokenAndReaps(): void
    {
        $client = $this->healthyClient();
        $engine = KitEngine::start(['binPath' => $this->fakeKit(self::STARTUP), 'client' => $client]);
        $engine->stop();

        $req = $client->last();
        self::assertSame('POST', $req->getMethod());
        self::assertSame('/shutdown', $req->getUri()->getPath());
        self::assertSame('Bearer stok', $req->getHeaderLine('Authorization'));

        $n = \count($client->requests);
        $engine->stop(); // idempotent once reaped
        self::assertCount($n + 1, $client->requests);
    }

    public function testStopWaitsForProcessExitAndReaps(): void
    {
        $engine = KitEngine::start(['binPath' => $this->fakeKit(self::STARTUP), 'client' => $this->healthyClient()]);
        $pid = (int) file_get_contents($this->dir . '/pid');
        self::assertTrue(posix_kill($pid, 0), 'child alive before stop');

        $engine->stop();

        // proc_close reaped it: no live process and no zombie left
        self::assertFalse(posix_kill($pid, 0), 'child gone after stop');
    }

    public function testStopFallsBackToAuthTokenForConnectedEngine(): void
    {
        $client = new RecordingClient(static fn () => new Response(204));
        $engine = KitEngine::connect(9090, 'tok', null, $client);
        $engine->stop();

        self::assertSame('Bearer tok', $client->last()->getHeaderLine('Authorization'));
    }

    public function testStopThrowsOnRejectedShutdown(): void
    {
        $client = new RecordingClient(static fn (RequestInterface $r) => $r->getUri()->getPath() === '/shutdown'
            ? RecordingClient::json(401, ['status' => 401])
            : RecordingClient::json(200, ['pid' => 1]));
        $engine = KitEngine::connect(9090, 'tok', 'bad', $client);

        $this->expectException(HttpException::class);
        $engine->stop();
    }

    public function testModuleAccessorsAreMemoized(): void
    {
        $engine = KitEngine::connect(9090, 't', null, new RecordingClient(static fn () => RecordingClient::json(200, ['pid' => 1])));

        self::assertInstanceOf(SyncClient::class, $engine->sync());
        self::assertSame($engine->sync(), $engine->sync());
        self::assertInstanceOf(PeerClient::class, $engine->peers());
        self::assertSame($engine->peers(), $engine->peers());
        self::assertInstanceOf(IdentityClient::class, $engine->identity());
        self::assertSame($engine->identity(), $engine->identity());
    }

    public function testModulesSendBearerOnWrites(): void
    {
        $client = new RecordingClient(static fn () => RecordingClient::json(200, ['pid' => 1, 'remotes' => []]));
        $engine = KitEngine::connect(9090, 'tok', null, $client);

        $engine->sync()->addRemote('origin', 'https://x', 'push');
        $req = $client->last();
        self::assertSame('POST', $req->getMethod());
        self::assertSame('/sync/remotes', $req->getUri()->getPath());
        self::assertSame('Bearer tok', $req->getHeaderLine('Authorization'));
        self::assertSame(
            ['name' => 'origin', 'url' => 'https://x', 'mode' => 'push', 'filter' => ''],
            json_decode((string) $req->getBody(), true),
        );

        $engine->peers()->trust('a b');
        self::assertSame('/peers/a%20b/trust', $client->last()->getUri()->getPath());

        $engine->identity()->verify('d', 's');
        self::assertSame('/identity/verify', $client->last()->getUri()->getPath());

        self::assertSame([], $engine->sync()->status());
        self::assertSame('/sync/status', $client->last()->getUri()->getPath());
    }

    public function testEventsAccessorReturnsLazyStream(): void
    {
        $engine = KitEngine::connect(9090, null, null, new RecordingClient(static fn () => RecordingClient::json(200, ['pid' => 1])));
        $stream = $engine->events('notes.*');
        self::assertInstanceOf(\HopTop\KitEngine\EventStream::class, $stream);
        self::assertSame('notes.*', $stream->topic);
    }

    public function testRealKitStartHealthStop(): void
    {
        $bin = getenv('KIT_BIN');
        if ($bin === false || $bin === '') {
            self::markTestSkipped('KIT_BIN not set');
        }
        $engine = KitEngine::start([
            'binPath' => $bin,
            'app' => 'phpsdk' . bin2hex(random_bytes(3)),
            'data' => $this->dir,
            'noPeer' => true,
            'noSync' => true,
        ]);
        self::assertGreaterThan(0, $engine->port);
        self::assertNotSame('', (string) $engine->token);
        $again = KitEngine::connect($engine->port, $engine->token, $engine->shutdownToken);
        self::assertSame($engine->pid, $again->pid);
        $engine->stop();

        $this->expectException(KitEngineException::class);
        KitEngine::connect($engine->port);
    }
}
