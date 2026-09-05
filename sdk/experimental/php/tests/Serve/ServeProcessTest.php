<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Serve\Signals;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\TestCase;

/**
 * Serving in a real process.
 *
 * Every other test drives the supervisor with a cancellation that
 * trips on its own, so all of them would pass even if `serve` returned
 * immediately without ever serving. Only a subprocess separates "the
 * run returned" from "the tool served": it asserts the process is
 * still alive after readiness, that SIGTERM starts a drain that exits
 * 0, and that the refusals carry their contract exit codes out to a
 * real shell status.
 */
#[Group('process')]
final class ServeProcessTest extends TestCase
{
    private const string ENTRY = __DIR__ . '/fixtures/serve-entry.php';

    /** @var list<string> */
    private array $scratch = [];

    protected function setUp(): void
    {
        if (!Signals::available()) {
            $this->markTestSkipped(
                'pcntl signal functions unavailable: no SIGTERM to deliver',
            );
        }
    }

    protected function tearDown(): void
    {
        foreach ($this->scratch as $f) {
            if (is_file($f)) {
                unlink($f);
            }
        }
    }

    public function testStaysAliveWhileServingAndExitsZeroOnSigterm(): void
    {
        $readyFile = tempnam(sys_get_temp_dir(), 'kit-serve-ready-');
        $this->assertIsString($readyFile);
        $this->scratch[] = $readyFile;
        // Truncate: the service writes it, and its existence alone must
        // not be the readiness signal.
        file_put_contents($readyFile, '');

        [$proc, $pipes] = $this->spawn(['serve', 'api'], ['SERVE_READY_FILE' => $readyFile]);

        try {
            $this->assertTrue(
                $this->waitForReady($readyFile),
                'service never reported ready in the child process',
            );

            // Still running: the supervisor held the process open
            // rather than falling off the end of the script. A process
            // that exits 0 without listening is indistinguishable from
            // a successful start to systemd or a container runtime.
            $status = proc_get_status($proc);
            $this->assertTrue($status['running'], 'supervisor exited instead of serving');

            // Give it a beat of real serving, then ask it to drain.
            usleep(150_000);
            $this->assertTrue(proc_get_status($proc)['running'], 'supervisor died while serving');

            proc_terminate($proc, SIGTERM);
            $code = $this->waitForExit($proc);

            // A signal-initiated stop is a clean stop. Answering
            // SIGTERM non-zero makes every rolling restart look like a
            // crash.
            $this->assertSame(0, $code);
        } finally {
            $this->reap($proc, $pipes);
        }
    }

    public function testASecondSignalEscalatesAndExitsNonZero(): void
    {
        $readyFile = tempnam(sys_get_temp_dir(), 'kit-serve-ready-');
        $this->assertIsString($readyFile);
        $this->scratch[] = $readyFile;
        file_put_contents($readyFile, '');

        // Escalation only means anything against a drain slow enough
        // for a second signal to land during it. With an instant stop
        // the graceful path has already finished and exiting 0 is
        // correct, so the delay is what puts the process in the state
        // the contract is actually describing.
        [$proc, $pipes] = $this->spawn(['serve', 'api'], [
            'SERVE_READY_FILE' => $readyFile,
            'SERVE_STOP_DELAY' => '3',
        ]);

        try {
            $this->assertTrue($this->waitForReady($readyFile));

            // Operators must be able to escalate without reaching for
            // SIGKILL, and the escalation abandons the drain.
            proc_terminate($proc, SIGTERM);
            usleep(200_000);
            proc_terminate($proc, SIGINT);

            $began = microtime(true);
            $code = $this->waitForExit($proc);
            $elapsed = microtime(true) - $began;

            $this->assertNotSame(0, $code, 'a second signal must not exit clean');
            // It abandoned the 3s drain rather than waiting it out.
            $this->assertLessThan(2.5, $elapsed, 'escalation did not abandon the drain');
        } finally {
            $this->reap($proc, $pipes);
        }
    }

    public function testExitsTwoOnTwoServiceOperands(): void
    {
        $this->assertSame(2, $this->runToCompletion(['serve', 'api', 'socket']));
    }

    public function testExitsThreeOnAnUnknownServiceOperand(): void
    {
        $this->assertSame(3, $this->runToCompletion(['serve', 'ghost']));
    }

    public function testExitsZeroForTheListFlag(): void
    {
        $this->assertSame(0, $this->runToCompletion(['serve', '--list']));
    }

    /**
     * @param list<string> $argv
     * @param array<string, string> $env
     * @return array{0: resource, 1: array<int, resource>}
     */
    private function spawn(array $argv, array $env = []): array
    {
        $cmd = array_merge([PHP_BINARY, self::ENTRY], $argv);
        $descriptors = [
            0 => ['pipe', 'r'],
            1 => ['pipe', 'w'],
            2 => ['pipe', 'w'],
        ];
        $proc = proc_open($cmd, $descriptors, $pipes, null, $env + ['PATH' => getenv('PATH')]);
        $this->assertIsResource($proc, 'failed to spawn the serve fixture');

        return [$proc, $pipes];
    }

    /** @param list<string> $argv */
    private function runToCompletion(array $argv): int
    {
        [$proc, $pipes] = $this->spawn($argv);
        try {
            return $this->waitForExit($proc);
        } finally {
            $this->reap($proc, $pipes);
        }
    }

    /** Polls for the child's readiness marker rather than sleeping blind. */
    private function waitForReady(string $file, float $budget = 10.0): bool
    {
        $deadline = microtime(true) + $budget;
        while (microtime(true) < $deadline) {
            clearstatcache(true, $file);
            if (is_file($file) && trim((string) file_get_contents($file)) === 'ready') {
                return true;
            }
            usleep(10_000);
        }
        return false;
    }

    /** @param resource $proc */
    private function waitForExit(mixed $proc, float $budget = 15.0): int
    {
        $deadline = microtime(true) + $budget;
        while (microtime(true) < $deadline) {
            $status = proc_get_status($proc);
            if ($status['running'] === false) {
                return $status['exitcode'];
            }
            usleep(10_000);
        }
        $this->fail('child did not exit within its budget');
    }

    /**
     * @param resource $proc
     * @param array<int, resource> $pipes
     */
    private function reap(mixed $proc, array $pipes): void
    {
        foreach ($pipes as $p) {
            if (is_resource($p)) {
                fclose($p);
            }
        }
        $status = proc_get_status($proc);
        if ($status['running'] === true) {
            // Narrowly targeted at this test's own child, by handle.
            proc_terminate($proc, SIGKILL);
        }
        proc_close($proc);
    }
}
