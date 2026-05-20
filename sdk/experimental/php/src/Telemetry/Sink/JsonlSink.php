<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry\Sink;

/**
 * Default transport: append JSONL lines to a per-PID file under XDG_STATE.
 *
 * Why JSONL is the default for PHP (vs. HTTPS-inline):
 *   * PHP has no first-class event loop. Long-lived async drains
 *     (asyncio / setImmediate / tokio::spawn) have no clean PHP
 *     equivalent that works in both CLI and FPM contexts.
 *   * JSONL append-on-shutdown is portable. In FPM the file write
 *     happens at `register_shutdown_function` time — after the response
 *     has been sent — so the request latency is not affected. In long-
 *     running CLI processes adopters can call `flush()` periodically
 *     and rely on the shutdown hook as a backstop.
 *   * A separate Go drain (the kit telemetry daemon) can sweep events
 *     off disk without coupling to the PHP process lifecycle.
 *
 * On-disk layout:
 *   $XDG_STATE_HOME/kit/telemetry/inbox/php-<pid>.jsonl
 *
 * Per-PID filenames avoid cross-process contention; LOCK_EX still wraps
 * the write so a concurrent reader (drain) sees consistent lines.
 *
 * Rotation: when an append crosses `sizeRotationBytes`, the file is
 * renamed to `<path>.1`. The drain process is responsible for further
 * promotion / cleanup; this sink keeps the policy intentionally simple.
 */
final class JsonlSink implements SinkInterface
{
    /** @var array<int, array<string, mixed>> */
    private array $queue = [];

    private readonly string $path;
    private readonly int $cap;
    private readonly int $sizeRotationBytes;

    private int $emitted = 0;
    private int $dropped = 0;
    private bool $shutdownRegistered = false;

    /**
     * @param string|null $path                Override target path; defaults
     *                                         to the per-PID XDG_STATE path.
     * @param int         $cap                 Max queued envelopes before
     *                                         enqueue() starts dropping.
     * @param int         $sizeRotationBytes   File-size trigger for rename
     *                                         to `<path>.1`. Default 10 MiB.
     * @param bool        $registerShutdown    Whether to register the
     *                                         shutdown flush. Tests pass
     *                                         false to control timing.
     */
    public function __construct(
        ?string $path = null,
        int $cap = 1024,
        int $sizeRotationBytes = 10 * 1024 * 1024,
        bool $registerShutdown = true,
    ) {
        $this->path = $path ?? self::defaultPath();
        $this->cap = $cap;
        $this->sizeRotationBytes = $sizeRotationBytes;

        if ($registerShutdown) {
            $this->registerShutdown();
        }
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

        $parent = dirname($this->path);
        if (!is_dir($parent)) {
            @mkdir($parent, 0o700, true);
        }

        $fp = @fopen($this->path, 'a');
        if ($fp === false) {
            // Transport unavailable; envelopes stay queued for the next
            // flush attempt. Don't drop here — the queue cap will if it
            // truly cannot drain.
            return;
        }

        if (!@flock($fp, LOCK_EX)) {
            @fclose($fp);

            return;
        }

        $written = 0;
        foreach ($this->queue as $envelope) {
            $line = json_encode($envelope, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
            if ($line === false) {
                $this->dropped++;
                continue;
            }
            $n = @fwrite($fp, $line . "\n");
            if ($n === false) {
                $this->dropped++;
                continue;
            }
            $this->emitted++;
            $written++;
        }
        $this->queue = [];

        $size = @ftell($fp);
        @flock($fp, LOCK_UN);
        @fclose($fp);

        if ($written > 0 && $size !== false && $size > $this->sizeRotationBytes) {
            // Best-effort rotate; if the rename fails (e.g. .1 exists and
            // permissions block overwrite), leave the file in place.
            @rename($this->path, $this->path . '.1');
        }
    }

    public function stats(): array
    {
        return [
            'emitted' => $this->emitted,
            'dropped' => $this->dropped,
            'queued' => count($this->queue),
            'path' => $this->path,
        ];
    }

    public function path(): string
    {
        return $this->path;
    }

    private function registerShutdown(): void
    {
        if ($this->shutdownRegistered) {
            return;
        }
        $this->shutdownRegistered = true;
        register_shutdown_function(function (): void {
            $this->flush();
        });
    }

    private static function defaultPath(): string
    {
        $stateHome = getenv('XDG_STATE_HOME');
        if ($stateHome === false || $stateHome === '') {
            $home = (string) ($_SERVER['HOME'] ?? getenv('HOME') ?: '');
            $stateHome = $home . '/.local/state';
        }

        $dir = $stateHome . '/kit/telemetry/inbox';

        return sprintf('%s/php-%d.jsonl', $dir, getmypid());
    }
}
