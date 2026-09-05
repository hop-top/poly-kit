<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * SIGINT/SIGTERM handling for the supervisor: the first signal begins
 * a graceful drain, a second of either kind escalates and abandons it.
 *
 * PHP delivers signals through ext/pcntl, which is not universally
 * present, so this class is written to degrade rather than to assume.
 *
 * Availability, established rather than assumed:
 *
 * - pcntl is compiled *into* the CLI binary on the Debian/Ubuntu
 *   packaging this repo's CI uses (`shivammathur/setup-php` on
 *   ubuntu-latest), so it needs no `extensions:` entry and no separate
 *   apt package. It is a documented builtin there.
 * - It does not exist on Windows at all, and hosting providers
 *   routinely blank the pcntl_* functions through `disable_functions`
 *   (Ondřej's own non-CLI ini does exactly that).
 * - `extension_loaded('pcntl')` is therefore NOT a usable probe: it
 *   still reports true when every function has been disabled. Only
 *   `function_exists()` reads the function table, which is the ground
 *   truth, so that is what available() asks.
 *
 * When the functions are missing, the contract's own escape clause
 * applies — "a port that cannot observe a *second* signal degrades to
 * the single graceful path rather than inventing a different
 * escalation". Here neither signal can be observed, so the supervisor
 * simply never receives one: `serve` still starts, serves, reports
 * readiness, and stops cleanly when its caller asks. What is lost is
 * only the ability for an operator's SIGTERM to *begin* that stop, and
 * available() lets a caller say so out loud rather than appear to have
 * installed handlers that silently do nothing.
 */
final class Signals
{
    /** The signals the supervisor listens for. SIGKILL is not catchable. */
    public const array SHUTDOWN_SIGNALS = [SIGINT, SIGTERM];

    private bool $stopped = false;
    private bool $installed = false;
    private int $count = 0;

    private function __construct(
        private readonly Cancellation $drain,
        private readonly Cancellation $escalation,
    ) {
    }

    /**
     * Reports whether this runtime can observe signals at all.
     *
     * Asks the function table rather than the extension list: a
     * `disable_functions` entry leaves the extension loaded while
     * making every call fatal.
     */
    public static function available(): bool
    {
        return function_exists('pcntl_signal')
            && function_exists('pcntl_async_signals')
            && function_exists('pcntl_signal_dispatch');
    }

    /**
     * Installs handlers for SIGINT and SIGTERM and returns the pair of
     * cancellations they drive.
     *
     * Returns a controller with no handlers installed when signals are
     * unavailable, so callers need no branch of their own; ask
     * installed() to find out which happened.
     */
    public static function install(): self
    {
        $c = new self(new Cancellation(), new Cancellation());
        if (!self::available()) {
            return $c;
        }

        // Without async delivery a signal would sit unhandled until
        // something called pcntl_signal_dispatch(), which the tick
        // loop does anyway — but async delivery also interrupts a
        // blocking call, which is what makes a drain start promptly.
        pcntl_async_signals(true);
        foreach (self::SHUTDOWN_SIGNALS as $sig) {
            pcntl_signal($sig, $c->onSignal(...));
        }
        $c->installed = true;
        return $c;
    }

    /** Whether handlers are actually installed on this runtime. */
    public function installed(): bool
    {
        return $this->installed;
    }

    /** Aborts on the first SIGINT/SIGTERM. */
    public function drain(): Cancellation
    {
        return $this->drain;
    }

    /** Aborts on a second signal of either kind. */
    public function escalation(): Cancellation
    {
        return $this->escalation;
    }

    /**
     * Delivers any signal that arrived and reports whether a drain has
     * been requested by the time it returns.
     *
     * Async delivery makes the dispatch redundant in the common case,
     * but a runtime with async delivery off still drains here, and it
     * costs nothing when nothing is pending.
     *
     * The drain state is returned rather than left for the caller to
     * re-read off the Cancellation: a signal handler flips that latch
     * asynchronously, with no assignment a static analyser can see, so
     * a re-read looks like dead code and invites deletion. A return
     * value is honest about the fact that calling this can change the
     * answer.
     */
    public function poll(): bool
    {
        if ($this->installed && !$this->stopped) {
            pcntl_signal_dispatch();
        }
        return $this->drain->cancelled();
    }

    /**
     * Restores the default disposition. Must be called, or the
     * handlers outlive the run — which matters because a second serve
     * run in one process would otherwise see the first run's handler.
     */
    public function stop(): void
    {
        if ($this->stopped || !$this->installed) {
            $this->stopped = true;
            return;
        }
        $this->stopped = true;
        foreach (self::SHUTDOWN_SIGNALS as $sig) {
            pcntl_signal($sig, SIG_DFL);
        }
    }

    /**
     * The first signal begins graceful shutdown; a second aborts the
     * drain, so an operator can escalate without reaching for SIGKILL.
     */
    private function onSignal(int $signal): void
    {
        if ($this->stopped) {
            return;
        }
        $this->count++;
        if ($this->count === 1) {
            $this->drain->cancel();
            return;
        }
        $this->escalation->cancel();
    }
}
