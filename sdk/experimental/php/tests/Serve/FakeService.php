<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Serve;

use HopTop\Kit\Serve\DeclaresAddress;
use HopTop\Kit\Serve\DeclaresClass;
use HopTop\Kit\Serve\DeclaresDependencies;
use HopTop\Kit\Serve\Service;
use HopTop\Kit\Serve\ValidatesConfig;
use RuntimeException;
use Throwable;

/**
 * A service whose every lifecycle decision the test dictates.
 *
 * Implements all four optional declarations so one class covers both
 * the "declares" and "does not declare" cases: each is inert until the
 * test supplies a value, and the supervisor asks the interface, not
 * the value, so a null still exercises the declaring branch. Cases
 * needing a genuinely non-declaring service use BareService.
 */
final class FakeService implements
    Service,
    ValidatesConfig,
    DeclaresDependencies,
    DeclaresAddress,
    DeclaresClass
{
    public int $startCount = 0;
    public int $stopCount = 0;
    public int $tickCount = 0;

    /** Order-of-events log shared across every service in one run. */
    public ?Recorder $recorder = null;

    /**
     * @param int $readyAfterTicks Ready only once this many ready()
     *        polls have gone by; 0 is ready immediately.
     * @param int|null $finishAfterTicks Return false from tick() once
     *        this many ticks have run, i.e. finish on its own.
     * @param list<string> $dependsOn
     * @param array{0: string, 1: string}|null $class
     */
    public function __construct(
        private readonly string $name,
        private readonly int $readyAfterTicks = 0,
        private readonly ?int $finishAfterTicks = null,
        private readonly ?Throwable $failStartWith = null,
        private readonly ?Throwable $failTickAfter = null,
        private readonly int $failTickAfterTicks = 0,
        private readonly ?Throwable $failStopWith = null,
        private readonly float $stopDelay = 0.0,
        private readonly ?string $configError = null,
        private readonly array $dependsOn = [],
        private readonly string $addr = '',
        private readonly ?array $class = null,
    ) {
    }

    public function name(): string
    {
        return $this->name;
    }

    public function start(): void
    {
        $this->startCount++;
        $this->recorder?->record('start', $this->name);
        if ($this->failStartWith !== null) {
            throw $this->failStartWith;
        }
    }

    /**
     * Ready once start() has run and the configured number of polls
     * has gone by. Counting polls is how a test says "not ready yet"
     * without a real clock: the supervisor polls in a loop, so a
     * service that needs N polls models one that needs time.
     */
    public function ready(): bool
    {
        if ($this->startCount === 0) {
            return false;
        }
        return $this->readyPolls++ >= $this->readyAfterTicks;
    }

    private int $readyPolls = 0;

    public function tick(): bool
    {
        $this->tickCount++;
        if ($this->failTickAfter !== null && $this->tickCount > $this->failTickAfterTicks) {
            throw $this->failTickAfter;
        }
        if ($this->finishAfterTicks !== null && $this->tickCount >= $this->finishAfterTicks) {
            return false;
        }
        return true;
    }

    public function stop(): void
    {
        $this->stopCount++;
        $this->recorder?->record('stop', $this->name);
        if ($this->stopDelay > 0.0) {
            usleep((int) ($this->stopDelay * 1_000_000));
        }
        if ($this->failStopWith !== null) {
            throw $this->failStopWith;
        }
    }

    public function validateConfig(): ?string
    {
        return $this->configError;
    }

    public function dependsOn(): array
    {
        return $this->dependsOn;
    }

    public function addr(): string
    {
        return $this->addr;
    }

    public function serviceClass(): array
    {
        return $this->class ?? ['', ''];
    }

    public static function failing(string $name, string $message): self
    {
        return new self($name, failStartWith: new RuntimeException($message));
    }
}
