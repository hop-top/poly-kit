<?php

declare(strict_types=1);

/**
 * A real tool that mounts `serve`, for the subprocess test.
 *
 * The unit tests drive the supervisor with a cancellation that trips
 * on its own, so they would pass even if the process never stayed up.
 * Only a subprocess proves the tool actually serves: it is the
 * difference between "the run returned" and "the tool served".
 *
 * Usage: php serve-entry.php serve [service...]
 */

use HopTop\Kit\Output\Builtins;
use HopTop\Kit\Output\Flags;
use HopTop\Kit\Output\Registry;
use HopTop\Kit\Serve\Service;
use HopTop\Kit\Serve\ServeCommand;
use HopTop\Kit\Serve\ServiceConfig;
use HopTop\Kit\Serve\ServiceRegistry;
use Symfony\Component\Console\Application;

require dirname(__DIR__, 3) . '/vendor/autoload.php';

/** A service that serves forever until it is stopped. */
final class ForeverService implements Service
{
    private bool $started = false;

    public function __construct(
        private readonly string $name,
        private readonly string $readyFile = '',
        private readonly float $stopDelay = 0.0,
    ) {
    }

    public function name(): string
    {
        return $this->name;
    }

    public function start(): void
    {
        $this->started = true;
        if ($this->readyFile !== '') {
            // Tells the parent the service is actually up, so the test
            // waits on a fact rather than on a sleep long enough to be
            // flaky on a loaded machine.
            file_put_contents($this->readyFile, "ready\n");
        }
    }

    public function ready(): bool
    {
        return $this->started;
    }

    public function tick(): bool
    {
        // Never finishes on its own: only a signal ends this process.
        usleep(10_000);
        return true;
    }

    public function stop(): void
    {
        // A drain slow enough for a second signal to land during it,
        // which is the only situation escalation is about.
        if ($this->stopDelay > 0.0) {
            usleep((int) ($this->stopDelay * 1_000_000));
        }
    }
}

$registry = new ServiceRegistry();
$registry->register(new ForeverService(
    'api',
    getenv('SERVE_READY_FILE') ?: '',
    (float) (getenv('SERVE_STOP_DELAY') ?: '0'),
));
$registry->register(new ForeverService('socket'));

$app = new Application('tool', '0.0.0-test');
$app->setAutoExit(false);

// --list renders through the kit output stack, which needs its flag
// suite on the application the same way a real adopter's main does.
$formatters = new Registry();
Builtins::register($formatters);
Flags::register($app);
Flags::setRegistry($app, $formatters);

$app->add(new ServeCommand(
    registry: $registry,
    configs: ['api' => new ServiceConfig(enabled: true)],
));

// Exit with the command's own status. Swallowing it into a blanket 0
// or 1 is exactly the masking this test exists to catch.
exit($app->run());
