<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Telemetry\Sink;

use HopTop\Kit\Telemetry\Sink\JsonlSink;
use PHPUnit\Framework\TestCase;

class JsonlSinkTest extends TestCase
{
    private string $tmpDir;

    protected function setUp(): void
    {
        $this->tmpDir = sys_get_temp_dir() . '/kit-jsonl-sink-' . bin2hex(random_bytes(8));
        mkdir($this->tmpDir, 0o700, true);
    }

    protected function tearDown(): void
    {
        $this->rmrf($this->tmpDir);
    }

    public function testEnqueueAndFlushRoundTrip(): void
    {
        $path = $this->tmpDir . '/test.jsonl';
        $sink = new JsonlSink($path, registerShutdown: false);

        $sink->enqueue(['event' => 'a', 'n' => 1]);
        $sink->enqueue(['event' => 'b', 'n' => 2]);

        $stats = $sink->stats();
        $this->assertSame(2, $stats['queued']);
        $this->assertSame(0, $stats['emitted']);

        $sink->flush();

        $stats = $sink->stats();
        $this->assertSame(0, $stats['queued']);
        $this->assertSame(2, $stats['emitted']);
        $this->assertSame(0, $stats['dropped']);

        $lines = array_values(array_filter(
            explode("\n", (string) file_get_contents($path)),
            static fn (string $l): bool => $l !== '',
        ));
        $this->assertCount(2, $lines);
        $this->assertSame(['event' => 'a', 'n' => 1], json_decode($lines[0], true));
        $this->assertSame(['event' => 'b', 'n' => 2], json_decode($lines[1], true));
    }

    public function testQueueOverflowDrops(): void
    {
        $path = $this->tmpDir . '/cap.jsonl';
        $sink = new JsonlSink($path, cap: 2, registerShutdown: false);

        $sink->enqueue(['n' => 1]);
        $sink->enqueue(['n' => 2]);
        $sink->enqueue(['n' => 3]); // dropped
        $sink->enqueue(['n' => 4]); // dropped

        $stats = $sink->stats();
        $this->assertSame(2, $stats['queued']);
        $this->assertSame(2, $stats['dropped']);

        $sink->flush();

        $lines = array_values(array_filter(
            explode("\n", (string) file_get_contents($path)),
            static fn (string $l): bool => $l !== '',
        ));
        $this->assertCount(2, $lines);
    }

    public function testFlushEmptyQueueNoOp(): void
    {
        $path = $this->tmpDir . '/empty.jsonl';
        $sink = new JsonlSink($path, registerShutdown: false);
        $sink->flush();
        $this->assertFileDoesNotExist($path);
        $this->assertSame(0, $sink->stats()['emitted']);
    }

    public function testCreatesParentDirectory(): void
    {
        $path = $this->tmpDir . '/nested/deep/file.jsonl';
        $sink = new JsonlSink($path, registerShutdown: false);
        $sink->enqueue(['hello' => 'world']);
        $sink->flush();
        $this->assertFileExists($path);
    }

    public function testSizeRotationRenamesPriorFile(): void
    {
        $path = $this->tmpDir . '/rotate.jsonl';
        // Tiny threshold so a single envelope crosses it.
        $sink = new JsonlSink($path, sizeRotationBytes: 8, registerShutdown: false);
        $sink->enqueue(['event' => 'big', 'data' => str_repeat('x', 32)]);
        $sink->flush();

        $this->assertFileExists($path . '.1');
        $this->assertFileDoesNotExist($path);
    }

    public function testAppendsToExistingFile(): void
    {
        $path = $this->tmpDir . '/append.jsonl';
        file_put_contents($path, "{\"pre\":1}\n");

        $sink = new JsonlSink($path, registerShutdown: false);
        $sink->enqueue(['post' => 2]);
        $sink->flush();

        $lines = array_values(array_filter(
            explode("\n", (string) file_get_contents($path)),
            static fn (string $l): bool => $l !== '',
        ));
        $this->assertCount(2, $lines);
        $this->assertSame(['pre' => 1], json_decode($lines[0], true));
        $this->assertSame(['post' => 2], json_decode($lines[1], true));
    }

    public function testStatsExposesPath(): void
    {
        $path = $this->tmpDir . '/p.jsonl';
        $sink = new JsonlSink($path, registerShutdown: false);
        $this->assertSame($path, $sink->stats()['path']);
        $this->assertSame($path, $sink->path());
    }

    public function testDefaultPathRespectsXdgStateHome(): void
    {
        $prev = getenv('XDG_STATE_HOME');
        putenv('XDG_STATE_HOME=' . $this->tmpDir);
        try {
            $sink = new JsonlSink(registerShutdown: false);
            $pid = getmypid();
            $this->assertSame(
                $this->tmpDir . '/kit/telemetry/inbox/php-' . $pid . '.jsonl',
                $sink->path(),
            );
        } finally {
            if ($prev === false) {
                putenv('XDG_STATE_HOME');
            } else {
                putenv('XDG_STATE_HOME=' . $prev);
            }
        }
    }

    private function rmrf(string $dir): void
    {
        if (!is_dir($dir)) {
            return;
        }
        $items = scandir($dir);
        if ($items === false) {
            return;
        }
        foreach ($items as $item) {
            if ($item === '.' || $item === '..') {
                continue;
            }
            $path = $dir . '/' . $item;
            if (is_dir($path) && !is_link($path)) {
                $this->rmrf($path);
            } else {
                @unlink($path);
            }
        }
        @rmdir($dir);
    }
}
