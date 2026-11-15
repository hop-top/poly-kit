<?php

declare(strict_types=1);

namespace HopTop\KitEngine;

use HopTop\KitEngine\Exception\HttpException;
use HopTop\KitEngine\Exception\KitEngineException;
use Psr\Http\Client\ClientInterface;

/**
 * Manages a `kit serve` sidecar and hands out module clients.
 *
 * Start options for {@see KitEngine::start()}:
 *  - app, data (string), port (int, 0 = ephemeral)
 *  - daemon, encrypt, noPeer, noSync (bool)
 *  - binPath (string): kit binary; default $KIT_BIN, else `kit` on PATH
 *  - client (ClientInterface): PSR-18 client; default Guzzle
 *  - startTimeout (float s, 10): wait for the startup line
 *  - healthTimeout (float s, 5): retry window for GET /health
 *  - stopTimeout (float s, 10): wait for exit after POST /shutdown
 */
final class KitEngine
{
    private const DEFAULT_START_TIMEOUT = 10.0;
    private const DEFAULT_HEALTH_TIMEOUT = 5.0;
    private const DEFAULT_STOP_TIMEOUT = 10.0;

    private readonly Http $http;
    /** @var array<string,Collection> */
    private array $collections = [];
    private ?SyncClient $sync = null;
    private ?PeerClient $peers = null;
    private ?IdentityClient $identity = null;

    /** @var resource|null */
    private $process;
    /** @var array<int,resource> */
    private array $pipes = [];

    /**
     * @param resource|null $process
     * @param array<int,resource> $pipes
     */
    private function __construct(
        public readonly int $port,
        public readonly int $pid,
        public readonly ?string $token,
        public readonly ?string $shutdownToken,
        private readonly ClientInterface $client,
        private readonly float $stopTimeout = self::DEFAULT_STOP_TIMEOUT,
        $process = null,
        array $pipes = [],
    ) {
        $this->http = new Http($client, self::baseUrlFor($port), $token);
        $this->process = $process;
        $this->pipes = $pipes;
    }

    /** @param array<string,mixed> $opts */
    public static function start(array $opts = []): self
    {
        $bin = $opts['binPath'] ?? (getenv('KIT_BIN') ?: 'kit');
        $cmd = [$bin, 'serve', '--port', (string) ($opts['port'] ?? 0)];
        foreach (['app' => '--app', 'data' => '--data'] as $key => $flag) {
            if (isset($opts[$key]) && $opts[$key] !== '') {
                array_push($cmd, $flag, (string) $opts[$key]);
            }
        }
        foreach (['daemon' => '--daemon', 'encrypt' => '--encrypt', 'noPeer' => '--no-peer', 'noSync' => '--no-sync'] as $key => $flag) {
            if (!empty($opts[$key])) {
                $cmd[] = $flag;
            }
        }
        $client = $opts['client'] ?? Http::defaultClient();

        $spec = [
            0 => ['file', PHP_OS_FAMILY === 'Windows' ? 'NUL' : '/dev/null', 'r'],
            1 => ['pipe', 'w'],
            2 => ['file', PHP_OS_FAMILY === 'Windows' ? 'NUL' : '/dev/null', 'w'],
        ];
        $pipes = [];
        $proc = @proc_open($cmd, $spec, $pipes, null, null, ['bypass_shell' => true]);
        if (!\is_resource($proc)) {
            throw new KitEngineException("kit serve: cannot execute {$bin}");
        }

        $engine = null;
        try {
            $line = self::readStartupLine($proc, $pipes[1], (float) ($opts['startTimeout'] ?? self::DEFAULT_START_TIMEOUT));
            $info = json_decode($line, true);
            if (!\is_array($info) || !isset($info['port'], $info['pid']) || !\is_int($info['port']) || !\is_int($info['pid'])) {
                throw new KitEngineException("kit serve: invalid startup JSON: {$line}");
            }
            $engine = new self(
                $info['port'],
                $info['pid'],
                isset($info['token']) ? (string) $info['token'] : null,
                isset($info['shutdown_token']) ? (string) $info['shutdown_token'] : null,
                $client,
                (float) ($opts['stopTimeout'] ?? self::DEFAULT_STOP_TIMEOUT),
                $proc,
                $pipes,
            );
            $engine->awaitHealth((float) ($opts['healthTimeout'] ?? self::DEFAULT_HEALTH_TIMEOUT));

            return $engine;
        } catch (\Throwable $e) {
            if ($engine !== null) {
                $engine->release(0.0);
            } else {
                self::reap($proc, $pipes, 0.0);
            }
            throw $e;
        }
    }

    public static function connect(
        int $port,
        ?string $token = null,
        ?string $shutdownToken = null,
        ?ClientInterface $client = null,
    ): self {
        $client ??= Http::defaultClient();
        $health = (new Http($client, self::baseUrlFor($port)))->request('GET', '/health');
        $pid = \is_array($health) && isset($health['pid']) ? (int) $health['pid'] : 0;

        return new self($port, $pid, $token, $shutdownToken, $client);
    }

    public function collection(string $type): Collection
    {
        return $this->collections[$type] ??= new Collection($this->client, self::baseUrlFor($this->port), $type, $this->token);
    }

    /**
     * The `/events` WebSocket stream is not implemented: the package ships
     * no WebSocket client (see composer `suggest`).
     */
    public function events(string $topic = '*'): never
    {
        throw new \LogicException("events({$topic}): WebSocket event streaming is not implemented in the PHP SDK");
    }

    public function sync(): SyncClient
    {
        return $this->sync ??= new SyncClient($this->http);
    }

    public function peers(): PeerClient
    {
        return $this->peers ??= new PeerClient($this->http);
    }

    public function identity(): IdentityClient
    {
        return $this->identity ??= new IdentityClient($this->http);
    }

    /** POST /shutdown, then wait for the spawned process to exit. */
    public function stop(): void
    {
        try {
            $this->http->request('POST', '/shutdown', null, [], $this->shutdownToken ?? $this->token);
        } catch (HttpException $e) {
            $this->release(0.0);
            throw $e;
        }
        $this->release($this->stopTimeout);
    }

    public function __destruct()
    {
        $this->release(0.0);
    }

    private function release(float $timeout): void
    {
        if ($this->process === null) {
            return;
        }
        self::reap($this->process, $this->pipes, $timeout);
        $this->process = null;
        $this->pipes = [];
    }

    private function awaitHealth(float $timeout): void
    {
        $deadline = microtime(true) + $timeout;
        do {
            try {
                $this->http->request('GET', '/health');

                return;
            } catch (KitEngineException $e) {
                $last = $e;
            }
            usleep(100_000);
        } while (microtime(true) < $deadline);

        throw new KitEngineException('kit serve health check failed: ' . $last->getMessage(), 0, $last);
    }

    /**
     * @param resource $proc
     * @param resource $stdout
     */
    private static function readStartupLine($proc, $stdout, float $timeout): string
    {
        stream_set_blocking($stdout, false);
        $buf = '';
        $deadline = microtime(true) + $timeout;
        while (($nl = strpos($buf, "\n")) === false) {
            $left = $deadline - microtime(true);
            if ($left <= 0) {
                throw new KitEngineException('kit serve: timed out waiting for startup JSON');
            }
            $r = [$stdout];
            $w = $e = null;
            $ready = @stream_select($r, $w, $e, (int) $left, (int) (fmod($left, 1) * 1_000_000));
            if ($ready === false) {
                throw new KitEngineException('kit serve: cannot read stdout');
            }
            $chunk = $ready > 0 ? fread($stdout, 8192) : '';
            if ($chunk !== false && $chunk !== '') {
                $buf .= $chunk;
            } elseif (feof($stdout)) {
                $code = proc_get_status($proc)['exitcode'];
                throw new KitEngineException("kit serve exited with {$code} before startup JSON");
            }
        }

        return substr($buf, 0, $nl);
    }

    /**
     * Wait up to $timeout for exit, then terminate, close pipes, reap.
     *
     * @param resource $proc
     * @param array<int,resource> $pipes
     */
    private static function reap($proc, array $pipes, float $timeout): void
    {
        if (!self::waitExit($proc, $timeout)) {
            proc_terminate($proc);
            if (!self::waitExit($proc, 2.0)) {
                proc_terminate($proc, 9);
            }
        }
        foreach ($pipes as $p) {
            if (\is_resource($p)) {
                fclose($p);
            }
        }
        proc_close($proc);
    }

    /** @param resource $proc */
    private static function waitExit($proc, float $timeout): bool
    {
        $deadline = microtime(true) + $timeout;
        while (proc_get_status($proc)['running']) {
            if (microtime(true) >= $deadline) {
                return false;
            }
            usleep(20_000);
        }

        return true;
    }

    private static function baseUrlFor(int $port): string
    {
        return "http://127.0.0.1:{$port}";
    }
}
