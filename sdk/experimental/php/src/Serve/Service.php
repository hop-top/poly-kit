<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * One long-running thing a tool can serve.
 *
 * The four required capabilities are the contract's minimum: a name, a
 * start that runs until cancelled or failed, a readiness report, and a
 * stop. Go expresses them as an interface and TypeScript as properties
 * on a plain object; the contract fixes the capability set and each
 * one's behavior, not a method table, so PHP uses the interface its
 * own idiom reaches for first.
 *
 * PHP has no event loop and no awaitable, so `start()` cannot be a
 * blocking call the supervisor races against a timer the way the Go
 * and TS ports do — a blocked `start()` would never return control and
 * nothing could observe readiness, a signal, or another service. The
 * shape here is cooperative instead: `start()` performs the
 * acquisitions that can fail deterministically and returns, and the
 * supervisor drives `tick()` in a loop until cancellation. The
 * observable behavior the contract names — ready at most once, the
 * ordered stop, the budgets, the exit codes — is identical; only the
 * mechanism differs, which the contract explicitly leaves to the port.
 */
interface Service
{
    /**
     * Stable service identifier. Must satisfy Names::validate() and
     * must not change across releases: renaming one is a breaking
     * change to the command surface, the config file, and any
     * subscriber filtering on the topic.
     */
    public function name(): string;

    /**
     * Acquires everything that can fail deterministically — binds the
     * listener, creates the socket file, attaches the subscription —
     * and returns.
     *
     * Throwing is how a start fails. Returning normally means the
     * acquisitions succeeded; it does NOT by itself mean the service
     * is ready. A service signals readiness by returning true from
     * ready() (typically straight after a successful start), and one
     * that never does inside its ready_timeout is a start failure.
     */
    public function start(): void;

    /**
     * Whether the service is currently accepting work. Readiness, not
     * liveness: a ready service may be idle, and may later fail.
     */
    public function ready(): bool;

    /**
     * Performs one slice of work and returns. The supervisor calls it
     * repeatedly between readiness and shutdown, so an implementation
     * must not block indefinitely: a long block delays every other
     * service's stop and the process's response to a signal.
     *
     * Returning false means the service has finished on its own and
     * wants no further slices. Throwing is a runtime crash, which the
     * failure policy then decides the fate of.
     */
    public function tick(): bool;

    /**
     * Drains in-flight work and releases resources. The supervisor
     * bounds it by the stop timeout and abandons a stop that exceeds
     * it, so an implementation must not assume it will be allowed to
     * finish.
     */
    public function stop(): void;
}
