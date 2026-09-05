<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Serve\Signals;
use PHPUnit\Framework\TestCase;

/**
 * Signal availability and its degraded path.
 *
 * Signals are the one place the contract allows a port to be unable to
 * conform, so what matters is that this port detects the condition
 * honestly rather than appearing to install handlers that do nothing.
 */
final class SignalsTest extends TestCase
{
    public function testListensForExactlySiginTandSigterm(): void
    {
        // SIGKILL and SIGSTOP are not catchable and are out of contract.
        $this->assertSame([SIGINT, SIGTERM], Signals::SHUTDOWN_SIGNALS);
    }

    public function testAvailabilityIsProbedThroughTheFunctionTableNotTheExtensionList(): void
    {
        // The trap this pins: `disable_functions` leaves the extension
        // LOADED while blanking every function in it, so
        // extension_loaded() reports a capability the process does not
        // have. Only function_exists() reads the function table, which
        // is what actually decides whether the call is fatal.
        //
        // Asserted in a child process, because disable_functions is
        // PHP_INI_SYSTEM and cannot be set once PHP is running.
        $script = <<<'PHP'
            require %s;
            $r = [
                'extension_loaded' => extension_loaded('pcntl'),
                'available' => \HopTop\Kit\Serve\Signals::available(),
                'installed' => \HopTop\Kit\Serve\Signals::install()->installed(),
            ];
            echo json_encode($r);
            PHP;
        $script = sprintf($script, var_export(dirname(__DIR__, 2) . '/vendor/autoload.php', true));

        $cmd = sprintf(
            '%s -d disable_functions=pcntl_signal,pcntl_async_signals,pcntl_signal_dispatch -r %s',
            escapeshellarg(PHP_BINARY),
            escapeshellarg($script),
        );
        $out = shell_exec($cmd);
        $this->assertIsString($out);

        /** @var array{extension_loaded: bool, available: bool, installed: bool} $got */
        $got = json_decode($out, true, 512, JSON_THROW_ON_ERROR);

        // The extension is still loaded — which is precisely why it is
        // the wrong thing to ask.
        $this->assertTrue($got['extension_loaded']);
        // And the honest answer is that signals are unavailable.
        $this->assertFalse($got['available']);
        // So install() reports it installed nothing, instead of
        // pretending a drain will ever start.
        $this->assertFalse($got['installed']);
    }

    public function testInstallReportsWhetherHandlersAreReallyInstalled(): void
    {
        $signals = Signals::install();
        try {
            $this->assertSame(Signals::available(), $signals->installed());
        } finally {
            $signals->stop();
        }
    }

    public function testStopIsIdempotent(): void
    {
        $signals = Signals::install();
        $signals->stop();
        $signals->stop();
        $this->assertFalse($signals->drain()->cancelled());
    }

    public function testDrainAndEscalationStartUncancelled(): void
    {
        $signals = Signals::install();
        try {
            $this->assertFalse($signals->drain()->cancelled());
            $this->assertFalse($signals->escalation()->cancelled());
        } finally {
            $signals->stop();
        }
    }
}
