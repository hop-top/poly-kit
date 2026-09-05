<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * The resolved `services.<name>` block for one service. Only the
 * lifecycle keys are modeled; service-specific keys live in the same
 * block and are read by the service itself.
 *
 * How a port *resolves* these keys is explicitly not contract — the
 * key names and their defaults are. This SDK has no dotted-key
 * precedence engine, so it reads the same key names out of a plain
 * array, which the contract permits in as many words.
 */
final class ServiceConfig
{
    /** `services.<name>.ready_timeout` default, in seconds. */
    public const float DEFAULT_READY_TIMEOUT = 30.0;
    /** `services.<name>.stop_timeout` default, in seconds. */
    public const float DEFAULT_STOP_TIMEOUT = 30.0;

    public function __construct(
        /**
         * `services.<name>.enabled`. Decides whether the supervisor
         * form starts this service. Defaults to false: a service that
         * starts listening because a dependency upgrade added it to
         * the registry is an unrequested open port.
         */
        public readonly bool $enabled = false,
        public readonly float $readyTimeout = self::DEFAULT_READY_TIMEOUT,
        public readonly float $stopTimeout = self::DEFAULT_STOP_TIMEOUT,
    ) {
    }

    /**
     * Reads one `services.<name>` block out of a resolved array,
     * using the contract's key names and defaults. Anything short of
     * an explicit true leaves the service disabled.
     *
     * @param array<string, mixed> $block
     */
    public static function fromArray(array $block): self
    {
        return new self(
            enabled: ($block['enabled'] ?? false) === true,
            readyTimeout: self::seconds($block['ready_timeout'] ?? null, self::DEFAULT_READY_TIMEOUT),
            stopTimeout: self::seconds($block['stop_timeout'] ?? null, self::DEFAULT_STOP_TIMEOUT),
        );
    }

    /**
     * Reads a whole `services` map into per-service blocks, skipping
     * the supervisor-scoped keys that live alongside them.
     *
     * @param array<string, mixed> $services
     * @return array<string, self>
     */
    public static function mapFromArray(array $services): array
    {
        $out = [];
        foreach ($services as $name => $block) {
            if (!is_array($block)) {
                continue;
            }
            /** @var array<string, mixed> $block */
            $out[$name] = self::fromArray($block);
        }
        return $out;
    }

    private static function seconds(mixed $raw, float $fallback): float
    {
        if (is_int($raw) || is_float($raw)) {
            return (float) $raw;
        }
        if (is_string($raw)) {
            $parsed = Duration::parse($raw);
            if ($parsed !== null) {
                return $parsed;
            }
        }
        return $fallback;
    }
}
