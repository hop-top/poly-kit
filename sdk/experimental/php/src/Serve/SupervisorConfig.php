<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * The supervisor-scoped half of the `services` block:
 * `services.failure_policy` and `services.shutdown_timeout`.
 */
final class SupervisorConfig
{
    /** Brings every service down when one fails. The default. */
    public const string POLICY_FAIL_FAST = 'fail-fast';
    /** Keeps the rest running when one fails. */
    public const string POLICY_ISOLATE = 'isolate';

    /** `services.shutdown_timeout` default, in seconds. */
    public const float DEFAULT_SHUTDOWN_TIMEOUT = 60.0;

    public function __construct(
        public readonly string $failurePolicy = self::POLICY_FAIL_FAST,
        public readonly float $shutdownTimeout = self::DEFAULT_SHUTDOWN_TIMEOUT,
    ) {
    }

    /** Reports whether $p is a declared failure policy. */
    public static function isFailurePolicy(string $p): bool
    {
        return $p === self::POLICY_FAIL_FAST || $p === self::POLICY_ISOLATE;
    }

    /**
     * Reads the supervisor-scoped keys out of a resolved `services`
     * map. An unrecognized failure policy falls back to the default
     * rather than failing the run: the key names are contract, and a
     * typo must not decide whether a process survives.
     *
     * @param array<string, mixed> $services
     */
    public static function fromArray(array $services): self
    {
        $policy = $services['failure_policy'] ?? null;
        $timeout = $services['shutdown_timeout'] ?? null;

        $seconds = self::DEFAULT_SHUTDOWN_TIMEOUT;
        if (is_int($timeout) || is_float($timeout)) {
            $seconds = (float) $timeout;
        } elseif (is_string($timeout)) {
            $seconds = Duration::parse($timeout) ?? self::DEFAULT_SHUTDOWN_TIMEOUT;
        }

        return new self(
            failurePolicy: is_string($policy) && self::isFailurePolicy($policy)
                ? $policy
                : self::POLICY_FAIL_FAST,
            shutdownTimeout: $seconds > 0 ? $seconds : self::DEFAULT_SHUTDOWN_TIMEOUT,
        );
    }
}
