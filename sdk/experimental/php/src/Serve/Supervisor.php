<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

use HopTop\Kit\Output\CliError;
use Throwable;

/**
 * Runs a resolved set of services under one lifecycle: ordered start,
 * per-service readiness, policy-driven reaction to failure, and
 * ordered stop bounded by the configured budgets.
 *
 * A Supervisor holds no static state, so two can run in one process
 * and a second run observes only its own cancellation — the contract's
 * re-execution rule, which costs this port nothing because a PHP
 * command object holds no context to inherit.
 *
 * The two command forms share this one implementation: a service
 * started by the selector observes the same readiness, shutdown, and
 * exit semantics as the same service started by the supervisor.
 */
final class Supervisor
{
    /** How long one tick sleeps when every service is idle. */
    private const int IDLE_SLEEP_US = 20_000;

    private readonly Emitter $emitter;

    /** @var callable():float */
    private $now;

    public function __construct(
        private readonly ServiceRegistry $registry,
        private readonly SupervisorConfig $config = new SupervisorConfig(),
        ?ServeLogger $logger = null,
        private readonly ?Cancellation $escalation = null,
        ?callable $now = null,
    ) {
        // The log is this SDK's only lifecycle sink, so a supervisor is
        // never constructed without one: staying silent about a service
        // that started, became ready, failed or stopped is the one
        // thing the contract forbids outright.
        $this->emitter = new Emitter($logger ?? new StderrLogger());
        $this->now = $now ?? static fn (): float => microtime(true);
    }

    /**
     * Starts every service in $selected, serves until cancelled, and
     * stops everything in reverse start order.
     *
     * Always performs the ordered stop before returning, so a caller
     * never has to clean up after it. $selected is normally
     * Resolution::$selected; run does not re-resolve and does not
     * consult enablement, because the decision the caller already made
     * is the one to honor.
     *
     * @param list<string> $selected
     * @param array<string, ServiceConfig> $configs
     */
    public function run(
        Cancellation $cancel,
        array $selected,
        array $configs = [],
        ?Signals $signals = null,
    ): RunResult {
        $state = new RunState($this->now);

        if ($selected === []) {
            return $this->finish($state, Outcome::NoServices, new CliError(
                code: CliError::CODE_USAGE,
                message: 'no services configured and enabled; enable one under '
                    . 'services.* or name one explicitly',
                exitCode: Outcome::NoServices->exitCode(),
                transience: CliError::TRANSIENCE_PERMANENT,
            ));
        }

        $order = StartOrder::of($this->registry, $selected);

        if (!$this->startAll($order, $configs, $state, $cancel)) {
            $this->emitAggregateReady($state);
            $this->serve($cancel, $state, $signals);
        }

        // Cancel once, so every service observes cancellation at the
        // same instant; nothing is queued behind another's drain.
        $cancel->cancel();
        $this->stopAll($state, $configs);

        return $this->finish($state, Outcome::worst($state->observed));
    }

    /**
     * Starts each service in order, waiting for each to report ready
     * (or fail, or exhaust its budget) before starting the next.
     *
     * Serial start is what makes dependsOn mean anything: a dependent
     * must not begin acquiring before its dependency is accepting
     * work. Returns true when a start failure short-circuits the
     * sequence.
     *
     * @param list<string> $order
     * @param array<string, ServiceConfig> $configs
     */
    private function startAll(
        array $order,
        array $configs,
        RunState $state,
        Cancellation $cancel,
    ): bool {
        foreach ($order as $name) {
            if ($cancel->cancelled()) {
                // Cancelled mid-sequence: what already started still
                // gets its ordered stop, and nothing further starts.
                return true;
            }

            $svc = $this->registry->lookup($name);
            if ($svc === null) {
                $this->fail($state, $name, "service \"{$name}\" disappeared from the registry",
                    Outcome::StartFailed, 'unregistered');
                return true;
            }

            $state->started[] = $name;
            $this->emitter->emit(Topics::OBJECT_SERVICE, Topics::ACTION_STARTED, [
                'service' => $name,
                'elapsed_ms' => $state->elapsedMs(),
            ]);

            try {
                $svc->start();
            } catch (Throwable $e) {
                $this->fail($state, $name, $e->getMessage(), Outcome::StartFailed, 'start', $e);
                return true;
            }

            if (!$this->awaitReady($svc, $name, $configs, $state, $cancel)) {
                return true;
            }
        }
        return false;
    }

    /**
     * Blocks until $name reports ready or exhausts its readiness
     * budget. A service that has not reported ready within the budget
     * is a start failure.
     *
     * @param array<string, ServiceConfig> $configs
     */
    private function awaitReady(
        Service $svc,
        string $name,
        array $configs,
        RunState $state,
        Cancellation $cancel,
    ): bool {
        $budget = ($configs[$name] ?? new ServiceConfig())->readyTimeout;
        $deadline = ($this->now)() + $budget;

        while (true) {
            try {
                if ($svc->ready()) {
                    $state->ready[] = $name;
                    $this->emitter->emit(Topics::OBJECT_SERVICE, Topics::ACTION_READY_REPORTED, [
                        'service' => $name,
                        'address' => $this->addrOf($svc),
                        'elapsed_ms' => $state->elapsedMs(),
                    ]);
                    return true;
                }
            } catch (Throwable $e) {
                $this->fail($state, $name, $e->getMessage(), Outcome::StartFailed, 'ready', $e);
                return false;
            }

            if (($this->now)() >= $deadline) {
                $this->fail(
                    $state,
                    $name,
                    sprintf('not ready within %gs', $budget),
                    Outcome::StartFailed,
                    'ready_timeout',
                );
                return false;
            }
            if ($cancel->cancelled()) {
                $this->fail($state, $name, 'cancelled before reporting ready',
                    Outcome::StartFailed, 'cancelled');
                return false;
            }
            usleep(self::IDLE_SLEEP_US);
        }
    }

    /**
     * Publishes the supervisor-scoped readiness event once every
     * started service is ready.
     */
    private function emitAggregateReady(RunState $state): void
    {
        if ($state->started !== [] && count($state->ready) === count($state->started)) {
            $this->emitter->emit(Topics::OBJECT_SUPERVISOR, Topics::ACTION_READY_REPORTED, [
                'elapsed_ms' => $state->elapsedMs(),
            ]);
        }
    }

    /**
     * Drives the services until cancellation, a failure trips the
     * policy, or every service has finished on its own.
     *
     * This is the loop that makes the process *stay up*. A `serve`
     * that returned here without blocking would exit 0 without ever
     * having served, which to systemd or a container runtime is
     * indistinguishable from a successful start.
     */
    private function serve(Cancellation $cancel, RunState $state, ?Signals $signals): void
    {
        $live = $state->started;

        while ($live !== [] && !$cancel->cancelled()) {
            // Deliver any pending signal. Async delivery usually beats
            // us to it, but a runtime without it still drains here —
            // and poll() reports the drain it just delivered, so the
            // loop reacts in the same iteration rather than serving one
            // more slice after the operator asked it to stop.
            if ($signals?->poll() === true) {
                return;
            }

            $worked = false;
            foreach ($live as $i => $name) {
                $svc = $this->registry->lookup($name);
                if ($svc === null) {
                    unset($live[$i]);
                    continue;
                }
                try {
                    if ($svc->tick() === false) {
                        // Finished on its own. Under either policy the
                        // process must not survive as an empty shell,
                        // so the loop ends when the last one is gone.
                        unset($live[$i]);
                        if (!$state->markStopped($name)) {
                            $this->emitter->emit(
                                Topics::OBJECT_SERVICE,
                                Topics::ACTION_STOPPED,
                                ['service' => $name, 'elapsed_ms' => $state->elapsedMs()],
                            );
                        }
                        continue;
                    }
                    $worked = true;
                } catch (Throwable $e) {
                    unset($live[$i]);
                    $this->fail($state, $name, $e->getMessage(),
                        Outcome::RuntimeCrash, 'runtime', $e);
                    if ($this->config->failurePolicy === SupervisorConfig::POLICY_FAIL_FAST) {
                        return;
                    }
                }
            }
            $live = array_values($live);

            if (!$worked) {
                // Nothing had work to do: yield rather than spin, so an
                // idle supervisor does not burn a core.
                usleep(self::IDLE_SLEEP_US);
            }
        }
    }

    /**
     * Invokes stop in the exact reverse of the order services actually
     * started, one at a time, so a dependent is always fully stopped
     * before its dependency.
     *
     * Each stop is bounded by that service's budget and by whatever
     * remains of the total. Exceeding the total ends the sequence with
     * shutdown-timeout.
     *
     * @param array<string, ServiceConfig> $configs
     */
    private function stopAll(RunState $state, array $configs): void
    {
        $order = $state->started;
        $deadline = ($this->now)() + $this->config->shutdownTimeout;

        for ($i = count($order) - 1; $i >= 0; $i--) {
            $name = $order[$i];

            // A second signal aborts the drain: the remaining services
            // are abandoned and the run exits with the crash code.
            if ($this->escalation?->cancelled() === true) {
                $state->observed[] = Outcome::RuntimeCrash;
                foreach (array_slice($order, 0, $i + 1) as $abandoned) {
                    $state->failed[$abandoned] = 'drain aborted by second signal';
                    $this->emitter->emit(Topics::OBJECT_SERVICE, Topics::ACTION_FAILED, [
                        'service' => $abandoned,
                        'error' => 'drain aborted by second signal',
                        'reason' => 'escalated',
                        'elapsed_ms' => $state->elapsedMs(),
                    ]);
                }
                return;
            }

            if (($this->now)() >= $deadline) {
                $state->observed[] = Outcome::ShutdownTimeout;
                $this->emitter->emit(Topics::OBJECT_SERVICE, Topics::ACTION_FAILED, [
                    'service' => $name,
                    'error' => sprintf(
                        'shutdown budget %gs exhausted before stopping',
                        $this->config->shutdownTimeout,
                    ),
                    'reason' => 'shutdown_timeout',
                    'elapsed_ms' => $state->elapsedMs(),
                ]);
                continue;
            }

            $svc = $this->registry->lookup($name);
            if ($svc === null) {
                continue;
            }
            $budget = min(
                ($configs[$name] ?? new ServiceConfig())->stopTimeout,
                max(0.0, $deadline - ($this->now)()),
            );
            $this->stopOne($svc, $name, $budget, $deadline, $state);
        }
    }

    /** Bounds one stop by its budget and by whatever remains of the total. */
    private function stopOne(
        Service $svc,
        string $name,
        float $budget,
        float $deadline,
        RunState $state,
    ): void {
        $began = ($this->now)();
        try {
            $svc->stop();
        } catch (Throwable $e) {
            $this->fail($state, $name, $e->getMessage(), Outcome::RuntimeCrash, 'stop', $e);
            return;
        }

        // PHP cannot preempt a synchronous stop, so a straggler is
        // detected after the fact rather than interrupted mid-call.
        // The contract's requirement is that one straggler must not
        // block the whole shutdown: the next service is stopped
        // regardless, and the overrun is surfaced as a failure the
        // same way an interruptible port would surface it.
        $elapsed = ($this->now)() - $began;
        if ($elapsed > $budget) {
            $overTotal = ($this->now)() >= $deadline;
            $this->fail(
                $state,
                $name,
                sprintf('stop exceeded %gs', $budget),
                $overTotal ? Outcome::ShutdownTimeout : Outcome::RuntimeCrash,
                $overTotal ? 'shutdown_timeout' : 'stop_timeout',
            );
            return;
        }

        // A service that returned on its own already reported stopped
        // when it did; the event is not repeated — one stopped per
        // service per run.
        if ($state->markStopped($name)) {
            return;
        }
        $this->emitter->emit(Topics::OBJECT_SERVICE, Topics::ACTION_STOPPED, [
            'service' => $name,
            'elapsed_ms' => $state->elapsedMs(),
        ]);
    }

    /** Records a failure and surfaces it under the `failed` transition. */
    private function fail(
        RunState $state,
        string $name,
        string $error,
        Outcome $outcome,
        string $reason,
        ?Throwable $source = null,
    ): void {
        $state->failed[$name] = $error;
        $state->observed[] = $outcome;
        if ($source !== null) {
            $state->sources[$name] = $source;
        }
        $this->emitter->emit(Topics::OBJECT_SERVICE, Topics::ACTION_FAILED, [
            'service' => $name,
            'error' => $error,
            'reason' => $reason,
            'elapsed_ms' => $state->elapsedMs(),
        ]);
    }

    private function addrOf(Service $svc): ?string
    {
        if (!$svc instanceof DeclaresAddress) {
            return null;
        }
        $addr = $svc->addr();
        return $addr === '' ? null : $addr;
    }

    /** Assembles the result from everything the run observed. */
    private function finish(RunState $state, Outcome $outcome, ?CliError $error = null): RunResult
    {
        $err = $error ?? ($outcome->isFailure() ? $this->failureError($outcome, $state) : null);

        $this->emitter->emit(Topics::OBJECT_SUPERVISOR, Topics::ACTION_STOPPED, [
            'reason' => $outcome->value,
            'elapsed_ms' => $state->elapsedMs(),
        ]);

        return new RunResult(
            outcome: $outcome,
            error: $err,
            started: $state->started,
            ready: $state->ready,
            failed: $state->failed,
        );
    }

    /**
     * Renders the outcome as the error envelope the command layer
     * returns, carrying the contract's code and exit code.
     *
     * The TRANSIENT propagation rule holds here: a failure wrapping a
     * kit transient error keeps exit 6, so an agent's retry branch
     * behaves the same whichever language the tool it is driving was
     * written in.
     */
    private function failureError(Outcome $outcome, RunState $state): CliError
    {
        $names = array_keys($state->failed);
        sort($names);

        $msg = match ($outcome) {
            Outcome::StartFailed => 'service failed to start',
            Outcome::ShutdownTimeout => 'shutdown budget exceeded',
            default => 'service failed',
        };
        foreach ($names as $i => $name) {
            $msg .= ($i === 0 ? ': ' : '; ') . $name . ': ' . $state->failed[$name];
        }

        foreach ($names as $name) {
            $source = $state->sources[$name] ?? null;
            if ($source instanceof TransientServiceException) {
                return new CliError(
                    code: CliError::CODE_TRANSIENT,
                    message: $msg,
                    exitCode: CliError::EXIT_TRANSIENT,
                    transience: CliError::TRANSIENCE_TRANSIENT,
                    source: $source,
                );
            }
        }

        return new CliError(
            code: $outcome->code(),
            message: $msg,
            exitCode: $outcome->exitCode(),
            transience: CliError::TRANSIENCE_PERMANENT,
        );
    }
}
